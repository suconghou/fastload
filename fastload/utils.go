package fastload

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

var (
	dialer = &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}
)

type DNSMapper struct {
	mappings map[string]string
}

// DialContext 是自定义的拨号函数，用于替换域名解析逻辑
func (m *DNSMapper) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	// 如果域名在映射表中，替换为指定 IP
	if ip, ok := m.mappings[host]; ok {
		addr = net.JoinHostPort(ip, port)
	}
	// 使用默认的 Dialer 建立连接, addr必须包含端口号
	return dialer.DialContext(ctx, network, addr)
}

// GenericPool 是一个泛型版的 sync.Pool
type GenericPool[T any] struct {
	pool sync.Pool
}

// NewGenericPool 创建一个泛型池
func NewGenericPool[T any](newFunc func() T) *GenericPool[T] {
	return &GenericPool[T]{
		pool: sync.Pool{
			New: func() any {
				return newFunc()
			},
		},
	}
}

// Get 从池中获取一个对象
func (p *GenericPool[T]) Get() T {
	// 如果池为空，底层会调用 New 函数，返回值一定不为 nil（如果是指针则是非 nil 指针）
	if v := p.pool.Get(); v != nil {
		return v.(T)
	}
	// 理论上走不到这里，为了编译器闭嘴和绝对安全加上
	var zero T
	return zero
}

// Put 将对象放回池中
func (p *GenericPool[T]) Put(v T) {
	p.pool.Put(v)
}

// onCloseReadCloser 是我们的装饰器结构体
type onCloseReadCloser struct {
	source      io.ReadCloser // 被包装的原始对象
	onCloseFunc func() error  // 在 Close 时额外调用的函数
}

// Read 方法直接调用原始 source 的 Read 方法
func (o *onCloseReadCloser) Read(p []byte) (n int, err error) {
	return o.source.Read(p)
}

// Close 方法会先调用我们自定义的函数，然后调用原始 source 的 Close 方法
func (o *onCloseReadCloser) Close() error {
	// 调用自定义的清理函数
	err1 := o.onCloseFunc()

	// 调用原始对象的 Close 方法
	err2 := o.source.Close()

	// 使用 errors.Join 合并两个操作可能返回的错误，这是最健壮的做法
	return errors.Join(err1, err2)
}

// NewOnCloseReadCloser 是一个工厂函数，用于创建我们的装饰器实例
// 注意它返回的是 io.ReadCloser 接口类型，而不是具体的 struct 类型，这是 Go 的惯例
func NewOnCloseReadCloser(rc io.ReadCloser, onClose func() error) io.ReadCloser {
	return &onCloseReadCloser{
		source:      rc,
		onCloseFunc: onClose,
	}
}
