package karta

const (
	defaultWorkerNum = 2
)

var (
	defaultMsgHandleFunc = func(msg any) (any, error) { return nil, nil }
)

type MessageHandleFunc func(msg any) (any, error)

type Callback interface {
	OnBefore(msg any)
	OnAfter(msg, result any, err error)
}

type emptyCallback struct{}

func (emptyCallback) OnBefore(msg any)                   {}
func (emptyCallback) OnAfter(msg, result any, err error) {}

type Config struct {
	num    int
	cb     Callback
	result bool
	h      MessageHandleFunc
}

func NewConfig() *Config {
	return &Config{
		num: defaultWorkerNum,
		cb:  &emptyCallback{},
		h:   defaultMsgHandleFunc,
	}
}

func (c *Config) WithWorkerNumber(num int) *Config {
	c.num = num
	return c
}

func (c *Config) WithCallback(cb Callback) *Config {
	c.cb = cb
	return c
}

func (c *Config) WithHandleFunc(h MessageHandleFunc) *Config {
	c.h = h
	return c
}

func (c *Config) WithResult() *Config {
	c.result = true
	return c
}

func DefaultConfig() *Config {
	return NewConfig()
}

func isConfigValid(conf *Config) *Config {
	if conf != nil {
		if conf.num <= 0 {
			conf.num = defaultWorkerNum
		}
		if conf.cb == nil {
			conf.cb = &emptyCallback{}
		}
		if conf.h == nil {
			conf.h = defaultMsgHandleFunc
		}
	} else {
		conf = NewConfig()
	}

	return conf
}
