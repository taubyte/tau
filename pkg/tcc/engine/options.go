package engine

import "errors"

func Path(path ...StringMatch) Option {
	return func(a *Attribute) {
		a.Path = path
	}
}

func Captures() Option {
	return func(a *Attribute) {
		a.Captures = true
	}
}

func Required() Option {
	return func(a *Attribute) {
		a.Required = true
	}
}

func Default[T any](val T) Option {
	return func(a *Attribute) {
		a.Default = val
	}
}

func Validator[T any](validator func(T) error) Option {
	return func(a *Attribute) {
		a.Validator = func(a any) error {
			switch b := a.(type) {
			case T:
				return validator(b)
			default:
				return errors.New("invalid type passed to validator")
			}
		}
	}
}
