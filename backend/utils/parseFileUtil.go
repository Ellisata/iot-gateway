package utils

import (
	"os"
	"strings"
)

// ParseDocx 解析 DOCX 文件（提取文本）
func ParseDocx(filePath string) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}
	// 简单提取：去除 XML 标签
	content := string(data)
	return stripXMLTags(content), nil
}

func stripXMLTags(s string) string {
	var result strings.Builder
	inTag := false
	for _, r := range s {
		if r == '<' {
			inTag = true
			continue
		}
		if r == '>' {
			inTag = false
			continue
		}
		if !inTag {
			result.WriteRune(r)
		}
	}
	return result.String()
}
