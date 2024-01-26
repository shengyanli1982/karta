package karta

const (
	// 默认的组中的工作者数量
	// default number of workers in a group.
	defaultWorkerNum = 2
)

var (
	// 默认的消息处理函数
	// default message handle function.
	DefaultMsgHandleFunc = func(msg any) (any, error) { return msg, nil }
)

// 消息处理函数
// message handle function.
type MessageHandleFunc func(msg any) (any, error)

// 配置
// config.
type Config struct {
	num        int               // number of workers
	callback   Callback          // callback
	result     bool              // return result
	handleFunc MessageHandleFunc // message handle function
}

// 创建一个新的配置
// create a new config.
func NewConfig() *Config {
	return &Config{
		num:        defaultWorkerNum,
		callback:   NewEmptyCallback(),
		handleFunc: DefaultMsgHandleFunc,
	}
}

// 设置工作者数量
// set number of workers.
func (c *Config) WithWorkerNumber(num int) *Config {
	c.num = num
	return c
}

// 设置回调函数
// set callback function.
func (c *Config) WithCallback(cb Callback) *Config {
	c.callback = cb
	return c
}

// 设置消息处理函数
// set message handle function.
func (c *Config) WithHandleFunc(h MessageHandleFunc) *Config {
	c.handleFunc = h
	return c
}

// 设置是否返回结果
// set whether return result.
func (c *Config) WithResult() *Config {
	c.result = true
	return c
}

// 创建一个默认的配置
// create a default config.
func DefaultConfig() *Config {
	return NewConfig()
}

// 检查配置是否有效，如果无效则返回一个默认的配置
// check config and return a default config if invalid.
func isConfigValid(conf *Config) *Config {
	if conf != nil {
		if conf.num <= 0 {
			conf.num = defaultWorkerNum
		}
		if conf.callback == nil {
			conf.callback = NewEmptyCallback()
		}
		if conf.handleFunc == nil {
			conf.handleFunc = DefaultMsgHandleFunc
		}
	} else {
		conf = DefaultConfig()
	}

	return conf
}
