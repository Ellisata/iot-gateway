package service

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"iot-gateway/appError"
	"iot-gateway/configFile"
	"iot-gateway/model/vo"
	"iot-gateway/response"
)

const (
	defaultTail = 200  // 默认返回末尾行数
	maxTail     = 5000 // 单次返回行数上限，防止超大文件一次读爆内存
	errLogFile  = "LOG_FILE_ERROR"
)

// LogFileService 日志文件查看服务（只读）。
// 日志目录取自配置 log.outputDir（见 configFile.LogConfig），默认 "log"。
type LogFileService struct {
	logDir string
}

// NewLogFileService 创建日志查看服务
func NewLogFileService(cfg *configFile.Config) *LogFileService {
	dir := "log"
	if cfg != nil && cfg.Log.OutputDir != "" {
		dir = cfg.Log.OutputDir
	}
	return &LogFileService{logDir: dir}
}

// ListFiles 分页列出日志目录下的 .log 文件（按修改时间倒序）。
// page 默认 1，size 默认 20、上限 100。
func (s *LogFileService) ListFiles(page, size int) (*response.PageVo[vo.LogFileVO], error) {
	if page <= 0 {
		page = 1
	}
	if size <= 0 {
		size = 20
	}
	if size > 100 {
		size = 100
	}

	entries, err := os.ReadDir(s.logDir)
	if err != nil {
		return nil, err
	}
	files := make([]vo.LogFileVO, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".log") {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		files = append(files, vo.LogFileVO{
			FileName:   e.Name(),
			Size:       info.Size(),
			ModifyTime: info.ModTime().Format("2006-01-02 15:04:05"),
		})
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].ModifyTime != files[j].ModifyTime {
			return files[i].ModifyTime > files[j].ModifyTime
		}
		// 修改时间相同时按文件名倒序，保证排序稳定（日志名按日期命名）
		return files[i].FileName > files[j].FileName
	})

	total := len(files)
	start := (page - 1) * size
	if start > total {
		start = total
	}
	end := start + size
	if end > total {
		end = total
	}
	return &response.PageVo[vo.LogFileVO]{
		Page:    page,
		Size:    size,
		Total:   int64(total),
		Records: files[start:end],
	}, nil
}

// ReadFile 读取日志文件末尾 tail 行。
// tail <=0 取默认值，超过 maxTail 时钳制为 maxTail。
func (s *LogFileService) ReadFile(fileName string, tail int) (*vo.LogReadVO, error) {
	if tail <= 0 {
		tail = defaultTail
	}
	if tail > maxTail {
		tail = maxTail
	}

	path, err := s.safePath(fileName)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, appError.NewAppError(errLogFile, "日志文件不存在")
	}
	defer f.Close()

	lines, err := tailLines(f, tail)
	if err != nil {
		return nil, err
	}
	total, err := countLines(f)
	if err != nil {
		return nil, err
	}
	return &vo.LogReadVO{
		FileName:   fileName,
		TotalLines: total,
		Lines:      lines,
	}, nil
}

// safePath 防目录穿越：只允许纯文件名（不含 /、\、..），
// 且解析后的绝对路径必须仍位于日志目录内。
func (s *LogFileService) safePath(name string) (string, error) {
	if name == "" || filepath.Base(name) != name {
		return "", appError.NewAppError(errLogFile, "日志文件名非法")
	}
	path := filepath.Join(s.logDir, name)
	rel, err := filepath.Rel(s.logDir, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", appError.NewAppError(errLogFile, "日志文件名非法")
	}
	return path, nil
}

// tailLines 从文件末尾向前分块反向扫描，返回最后 n 行。
// 用 ReadAt 不移动文件指针，避免整文件载入内存。
func tailLines(f *os.File, n int) ([]string, error) {
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := fi.Size()
	if size == 0 {
		return nil, nil
	}

	const blockSize = 8 * 1024
	pos := size
	var (
		lines   []string
		partial []byte // 跨块累积：当前块右侧尚未遇到换行符的行尾片段
	)

	for pos > 0 {
		readLen := int64(blockSize)
		if pos < readLen {
			readLen = pos
		}
		isFileTail := pos == size // 首块（文件末尾），用于跳过结尾空行
		pos -= readLen

		chunk := make([]byte, readLen)
		if _, err := f.ReadAt(chunk, pos); err != nil && err != io.EOF {
			return nil, err
		}

		// 从块尾向前找换行符，逐个拆出完整行
		end := len(chunk)
		firstEmission := true
		for end > 0 && len(lines) < n {
			idx := bytes.LastIndexByte(chunk[:end], '\n')
			if idx < 0 {
				break
			}
			seg := chunk[idx+1 : end]
			if len(partial) > 0 {
				seg = append(append([]byte{}, seg...), partial...)
				partial = nil
			}
			if firstEmission {
				firstEmission = false
				// 文件以 \n 结尾时的末尾空段不算一行
				if isFileTail && len(seg) == 0 {
					end = idx
					continue
				}
			}
			lines = append(lines, string(seg))
			end = idx
		}

		if len(lines) >= n {
			break
		}
		// 块内未拆完的左侧内容属于更早一行的行尾，并入 partial
		if end > 0 {
			partial = append(append([]byte{}, chunk[:end]...), partial...)
		}
	}

	// 文件首行（以非换行符结尾）的残留
	if len(partial) > 0 {
		lines = append(lines, string(partial))
	}

	// lines 是逆序收集的，反转后返回
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}
	if len(lines) > n {
		lines = lines[:n]
	}
	return lines, nil
}

// countLines 流式统计文件行数（从当前文件指针位置读取）。
func countLines(f *os.File) (int, error) {
	buf := make([]byte, 64*1024)
	count := 0
	for {
		n, err := f.Read(buf)
		count += bytes.Count(buf[:n], []byte{'\n'})
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, err
		}
	}
	// 末行未以换行符结尾时补 1 行
	if fi, err := f.Stat(); err == nil && fi.Size() > 0 {
		last := make([]byte, 1)
		if _, err := f.ReadAt(last, fi.Size()-1); err == nil && last[0] != '\n' {
			count++
		}
	}
	return count, nil
}
