package command

type Option func(c *Command) error

func Args(args ...string) Option {
	return func(c *Command) error {
		c.args = args
		return nil
	}
}

func Env(name, value string) Option {
	return func(c *Command) error {
		c.env[name] = value
		return nil
	}
}

// starts a login shell
func Shell() Option {
	return func(c *Command) error {
		return c.sess.Shell()
	}
}
