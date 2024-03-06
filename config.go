package karta

import "math"

// 定义默认的最小和最大工作者数量
// Define the default minimum and maximum number of workers
const (
	// 默认的最小工作者数量
	// Default minimum number of workers
	defaultMinWorkerNum = int64(1)

	// 默认的最大工作者数量
	// Default maximum number of workers
	defaultMaxWorkerNum = int64(math.MaxUint16) * 8
)

var (
	// 默认的消息处理函数，返回接收到的消息和nil错误
	// Default message handle function, returns the received message and a nil error
	DefaultMsgHandleFunc = func(msg any) (any, error) { return msg, nil }
)

// 定义消息处理函数类型
// Define the message handle function type
type MessageHandleFunc = func(msg any) (any, error)

// 定义配置类型
// Define the config type
type Config struct {
	num        int               // 工作者数量
	callback   Callback          // 回调函数
	result     bool              // 是否返回结果
	handleFunc MessageHandleFunc // 消息处理函数
}

// 创建一个新的配置，初始化工作者数量，回调函数和消息处理函数
// Create a new config, initialize the number of workers, callback function and message handle function
func NewConfig() *Config {
	return &Config{
		num:        int(defaultMinWorkerNum),
		callback:   NewEmptyCallback(),
		handleFunc: DefaultMsgHandleFunc,
	}
}

// 设置工作者数量
// Set the number of workers
func (c *Config) WithWorkerNumber(num int) *Config {
	c.num = num
	return c
}

// 设置回调函数
// Set the callback function
func (c *Config) WithCallback(callback Callback) *Config {
	c.callback = callback
	return c
}

// 设置消息处理函数
// Set the message handle function
func (c *Config) WithHandleFunc(fn MessageHandleFunc) *Config {
	c.handleFunc = fn
	return c
}

// 设置是否返回结果
// Set whether to return the result
func (c *Config) WithResult() *Config {
	c.result = true
	return c
}

// DefaultConfig 创建一个默认的配置
// DefaultConfig creates a default configuration
func DefaultConfig() *Config {
	// 调用 NewConfig 函数创建一个新的配置
	// Call the NewConfig function to create a new configuration
	return NewConfig()
}

// isConfigValid 检查配置是否有效，如果无效则返回一个默认的配置
// isConfigValid checks if the configuration is valid, if not, it returns a default configuration
func isConfigValid(conf *Config) *Config {
	// 如果配置不为 nil
	// If the configuration is not nil
	if conf != nil {
		// 如果工作者数量小于等于0或者大于默认的最大工作者数量
		// If the number of workers is less than or equal to 0 or greater than the default maximum number of workers
		if conf.num <= 0 || conf.num > int(defaultMaxWorkerNum) {
			// 设置工作者数量为默认的最小工作者数量
			// Set the number of workers to the default minimum number of workers
			conf.num = int(defaultMinWorkerNum)
		}
		// 如果回调函数为 nil
		// If the callback function is nil
		if conf.callback == nil {
			// 设置回调函数为一个空的回调函数
			// Set the callback function to an empty callback function
			conf.callback = NewEmptyCallback()
		}
		// 如果消息处理函数为 nil
		// If the message handling function is nil
		if conf.handleFunc == nil {
			// 设置消息处理函数为默认的消息处理函数
			// Set the message handling function to the default message handling function
			conf.handleFunc = DefaultMsgHandleFunc
		}
	} else {
		// 如果配置为 nil，创建一个默认的配置
		// If the configuration is nil, create a default configuration
		conf = DefaultConfig()
	}

	// 返回配置
	// Return the configuration
	return conf
}
