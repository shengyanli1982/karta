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

// Config 是一个结构体，用于配置消息处理的参数
// Config is a struct used to configure parameters for message processing
type Config struct {

	// num 是一个整数，表示工作者的数量
	// num is an integer that represents the number of workers
	num int

	// callback 是一个 Callback 类型的变量，表示消息处理前后的回调函数
	// callback is a variable of type Callback, which represents the callback function before and after message processing
	callback Callback

	// result 是一个布尔值，表示是否需要返回处理结果
	// result is a boolean value that indicates whether a processing result needs to be returned
	result bool

	// handleFunc 是一个 MessageHandleFunc 类型的变量，表示消息处理函数
	// handleFunc is a variable of type MessageHandleFunc, which represents the message handling function
	handleFunc MessageHandleFunc
}

// NewConfig 是一个函数，用于创建并返回一个新的 Config 结构体的指针
// NewConfig is a function that creates and returns a pointer to a new Config struct
func NewConfig() *Config {
	return &Config{
		// num 是工作线程的数量，默认为 defaultMinWorkerNum
		// num is the number of worker threads, default is defaultMinWorkerNum
		num: int(defaultMinWorkerNum),

		// callback 是一个 Callback 类型的变量，用于处理消息前后的回调函数，默认为空
		// callback is a variable of type Callback, used for callback functions before and after handling messages, default is empty
		callback: NewEmptyCallback(),

		// handleFunc 是一个 MessageHandleFunc 类型的变量，用于处理消息的函数，默认为 DefaultMsgHandleFunc
		// handleFunc is a variable of type MessageHandleFunc, used for the function to handle messages, default is DefaultMsgHandleFunc
		handleFunc: DefaultMsgHandleFunc,
	}
}

// WithWorkerNumber 是一个方法，用于设置 Config 结构体中的 num 变量
// WithWorkerNumber is a method used to set the num variable in the Config struct
func (c *Config) WithWorkerNumber(num int) *Config {
	c.num = num
	return c
}

// WithCallback 是一个方法，用于设置 Config 结构体中的 callback 变量
// WithCallback is a method used to set the callback variable in the Config struct
func (c *Config) WithCallback(callback Callback) *Config {
	c.callback = callback
	return c
}

// WithHandleFunc 是一个方法，用于设置 Config 结构体中的 handleFunc 变量
// WithHandleFunc is a method used to set the handleFunc variable in the Config struct
func (c *Config) WithHandleFunc(fn MessageHandleFunc) *Config {
	c.handleFunc = fn
	return c
}

// WithResult 是一个方法，用于设置 Config 结构体中的 result 变量
// WithResult is a method used to set the result variable in the Config struct
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
