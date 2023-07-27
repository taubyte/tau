package common

import (
	"context"
)

type Multiverse interface {
	Exist(universe string) bool
	Universe(name string) Universe
	Context() context.Context
	ValidServices() []string
	ValidFixtures() []string
	ValidClients() []string
	Status() interface{}
}
