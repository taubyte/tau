# Command utils

This module is for adding functionality to commands

Say you want a function to take a name, you define the command
```go
command := &cli.Command{
    Name: "some-command"
}
```
Now before returning if you want to attach an ability to this command

```go
command.Name(command)
```

This empty wrapping will attribute args[0] to name, and return an
error if the name is not found