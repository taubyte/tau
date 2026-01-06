# taubyte/go-simple-container 

[![Release](https://img.shields.io/github/release/taubyte/go-simple-container.svg)](https://github.com/taubyte/tau/pkg/containers/releases)
[![License](https://img.shields.io/github/license/taubyte/go-simple-container)](LICENSE)
[![Go Report Card](https://goreportcard.com/badge/taubyte/go-simple-container)](https://goreportcard.com/report/taubyte/go-simple-container)
[![GoDoc](https://godoc.org/github.com/taubyte/tau/pkg/containers?status.svg)](https://pkg.go.dev/github.com/taubyte/tau/pkg/containers)
[![Discord](https://img.shields.io/discord/973677117722202152?color=%235865f2&label=discord)](https://tau.link/discord)

An abstraction layer over the docker api client. Goal: make it simple to use containers from go.

## Installation 
The import path for the package is *github.com/taubyte/tau/pkg/containers*.

To install it, run:
```bash 
go get github.com/taubyte/tau/pkg/containers
```


## Usage

### Basic Example
```go
import (
    ci "github.com/taubyte/tau/pkg/containers"
    "context"
)

ctx := context.Background()

// Create an new client
client, err := ci.New()
if err != nil{
    return err
}

// Using `node` image for our container
dockerImage := "node"

// Initialize docker image with our given image name
image, err := client.Image(ctx, dockerImage)
if err != nil{
    return err
}

// Commands we will be running
commands := []string{"echo","Hello World!"}

// Mount Volume Option 
volume := ci.Volume("/sourcePath","/containerPath")

// Add Environment Variable Option
variable := ci.Variable("KEY","value")

// Instantiate the container with commands we will run
container, err := image.Instantiate(
    ctx,
    ci.Command(commands),
    // options
    volume, 
    variable
)
if err != nil{
    return err
}

// Run container 
logs, err := container.Run(ctx)
if err != nil{
    return err
}

// Create new byte buffer 
var buf bytes.Buffer

// Read logs 
buf.ReadFrom(logs.Combined())

// Set output to the string value of the buffer 
output := buf.String()

// Close the log Reader
logs.Close()

```

### Using Your Own Dockerfile
- Create a Dockerfile in a directory with any dependencies that you may need for the Dockerfile, the file must be named Dockerfile. This is case sensitive.
- run: `$ tar cvf <docker_tarball_name>.tar -C <directory>/ .`
- Docker expects Dockerfile and any files you need to build the container image inside a tar file.
    - Using embed: 
    ```go
    //go:embed <docker_tarball_name>.tar
    var tarballData []byte 
    
    imageOption := containers.Build(bytes.NewBuffer(tarballData))
    ```
    - Using a file:
    ```go 
    tarball, err := os.Open("<path_to>/<docker_tarball_name.tar>")
    if err != nil{
        return err
    }
    defer tarball.Close()

    imageOption := containers.Build(tarball)
    ```

- Create the image with a custom image name, and the the ImageOption
    - The image name must follow the convention `<Organization>/<Repo_Name>:Version`
    - All characters must be lower case 
```go
client.Image(context.Background(),"taubyte/testrepo:version1",imageOption)
```


### Creating a Garbage Collector
```go
import ( 
    "github.com/taubyte/tau/pkg/containers/gc"
    ci "github.com/taubyte/tau/pkg/containers" 
)

// Create new docker client 
client, err := ci.New()
if err != nil{
    return err
}

// Create a context with cancel func, calling the cancel func will end the garbage collector go routine.
ctx, ctxC := context.WithCancel(context.Background())

// Create new garbage collector
gc.New(ctx, gc.Interval(20 * time.Second), gc.MaxAge(10 *time.Second ))

```

## Running Tests 
If running tests for the first time, from directory run: 
```bash
cd tests 
go generate
```

Then run 
```bash
$ go test -v
```
