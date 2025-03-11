package context

func RawResponse() Option {
	return func(ctx *Context) error {
		ctx.rawResponse = true
		return nil
	}
}
