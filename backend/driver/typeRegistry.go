package driver

import (
	"fmt"
	"strings"
	"sync"
)

// globalRegistry 全局类型注册表
var globalRegistry = newTypeRegistry()

// GetTypeRegistry 返回全局类型注册表。
// 各协议驱动在 init() 中通过本接口注册 DataType。
func GetTypeRegistry() *TypeRegistry {
	return globalRegistry
}

// TypeRegistry 线程安全的数据类型注册表。
//
// 以内部类型名（小写，如 "bool", "int16"）为键存储 DataType；
// 协议专属类型经 ForProtocol 作用域注册（自动加 "{protocol}." 前缀）。
// 所有匹配不区分大小写。
type TypeRegistry struct {
	mu    sync.RWMutex
	types map[string]*DataType // 内部名 → DataType（key 已小写）
}

func newTypeRegistry() *TypeRegistry {
	return &TypeRegistry{
		types: make(map[string]*DataType),
	}
}

// Register 注册数据类型。name 重复时覆盖。
func (r *TypeRegistry) Register(dt *DataType) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.types[strings.ToLower(dt.Name)] = dt
}

// Get 根据名称查找数据类型（不区分大小写）。
func (r *TypeRegistry) Get(name string) (*DataType, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	dt, ok := r.types[strings.ToLower(name)]
	return dt, ok
}

// MustGet 同 Get，但类型不存在时 panic（适用于启动时校验）。
func (r *TypeRegistry) MustGet(name string) *DataType {
	dt, ok := r.Get(name)
	if !ok {
		panic(fmt.Sprintf("type registry: unknown data type %q", name))
	}
	return dt
}

// Lookup 查询类型信息，返回类型名（已标准化为内部名）、字节数和是否找到。
func (r *TypeRegistry) Lookup(name string) (internalName string, size int, ok bool) {
	dt, found := r.Get(name)
	if !found {
		return "", 0, false
	}
	// 自反查找内部名：types map 中 key 就是内部名
	r.mu.RLock()
	for n, d := range r.types {
		if d == dt {
			internalName = n
			break
		}
	}
	r.mu.RUnlock()
	return internalName, dt.Size, true
}

// Contains 快捷检查类型是否已注册。
func (r *TypeRegistry) Contains(name string) bool {
	_, ok := r.Get(name)
	return ok
}

// ForProtocol 返回指定协议的 scoped registry view。
// 通过 ProtocolScope 注册/查询的类型名自动添加 "{protocol}." 前缀，
// 避免不同协议注册同名类型（如 "date"）时互相覆盖。
func (r *TypeRegistry) ForProtocol(protocol string) *ProtocolScope {
	return &ProtocolScope{
		registry: r,
		prefix:   strings.ToLower(protocol) + ".",
	}
}

// ProtocolScope 协议作用域下的类型注册/查询视图。
// 注册时自动添加协议前缀，查找时优先查找带前缀的类型，未命中则回退到全局查找。
type ProtocolScope struct {
	registry *TypeRegistry
	prefix   string // "modbus.", "s7."
}

// Register 注册数据类型，Name 会被自动添加协议前缀。
// 例如 mb.Register(&DataType{Name: "bool", ...}) 实际注册为 "modbus.bool"。
func (s *ProtocolScope) Register(dt *DataType) {
	dt2 := *dt
	dt2.Name = s.prefix + dt.Name
	s.registry.Register(&dt2)
}

// Get 查找数据类型，优先查找带协议前缀的名称，未命中时回退到全局查找。
// 例如 mb.Get("bool") 先查 "modbus.bool"，若不存在则查全局 "bool"。
func (s *ProtocolScope) Get(name string) (*DataType, bool) {
	if dt, ok := s.registry.Get(s.prefix + name); ok {
		return dt, true
	}
	return s.registry.Get(name)
}

// MustGet 同 Get，但类型不存在时 panic。
func (s *ProtocolScope) MustGet(name string) *DataType {
	dt, ok := s.Get(name)
	if !ok {
		panic(fmt.Sprintf("type registry: unknown data type %q in protocol scope %q", name, s.prefix))
	}
	return dt
}

// KindOf 解析(裸)类型名对应的类别 Kind，供驱动在构建 ReadResult 时填充 Kind。
// name 为内部裸名（如 "int16"），经前缀解析命中本协议注册的类型；未命中返回 ""。
func (s *ProtocolScope) KindOf(name string) string {
	dt, ok := s.Get(name)
	if !ok {
		return ""
	}
	return dt.Kind
}

// Names 返回所有已注册的内部类型名。
func (r *TypeRegistry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.types))
	for name := range r.types {
		names = append(names, name)
	}
	return names
}
