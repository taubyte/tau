# yaseer (old taubyte/go-seer)

Yaseer is a tool to parse and edit YAML files in a directory as one structure.

## Features
 - Preserves comments and formatting of original document
 - Creates non existing documents
 - Maps folders and files to objects


Note: Under the hood we use *gopkg.in/yaml.v3* so YAML 1.1 & 1.2 are supported.

## Installation
The import path for the package is *github.com/taubyte/yaseer*.

To install it, run:
```bash
go get github.com/taubyte/tau/pkg/yaseer
```

## Usage
First start by creating an instance of yaseer
```go
s := New(SystemFS("config/"))
```

Note that you can also use a virtual file system.
```go
import "github.com/spf13/afero"

vfs := afero.NewMemMapFs()

s := New(VirtualFS(vfs,"config/"))
```

Now, let's build a query that will create a YAML file representing a leaf object:
```go
type EV struct {
    Battery int
    Range int
}

err = seer.Get("cars").Get("electric").Get("taumobile").Document().Set(EV{Battery: 100, Range:400}).Commit()
```

If you check the file system you will find
```
cars/
  electric/
    taumobile.yaml
```

Open `taumobile.yaml`
```
Battery: 100
Range: 400
```

To query a value

```go
var battery int
seer.Get("cars").Get("electric").Get("taumobile").Get("Battery").Value(&battery)
fmt.Printf("Battery of %dKwh\n", battery)
```

Will print
```
Battery of 100Kwh
```
