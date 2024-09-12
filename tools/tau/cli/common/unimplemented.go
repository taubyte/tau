package common

import "github.com/urfave/cli/v2"

var _ Basic = UnimplementedBasic{}
var NotImplemented Command = nil

type UnimplementedBasic struct{}

func (UnimplementedBasic) New() Command                   { return NotImplemented }
func (UnimplementedBasic) Edit() Command                  { return NotImplemented }
func (UnimplementedBasic) Delete() Command                { return NotImplemented }
func (UnimplementedBasic) Query() Command                 { return NotImplemented }
func (UnimplementedBasic) List() Command                  { return NotImplemented }
func (UnimplementedBasic) Select() Command                { return NotImplemented }
func (UnimplementedBasic) Clone() Command                 { return NotImplemented }
func (UnimplementedBasic) Push() Command                  { return NotImplemented }
func (UnimplementedBasic) Pull() Command                  { return NotImplemented }
func (UnimplementedBasic) Checkout() Command              { return NotImplemented }
func (UnimplementedBasic) Import() Command                { return NotImplemented }
func (UnimplementedBasic) Base() (*cli.Command, []Option) { return nil, nil }
