package helpers

import "context"

func New(ctx context.Context) Methods {
	return &methods{ctx}
}
