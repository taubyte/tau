# Tau Test Framework

The Tau Test Framework is a set of test cases and utilities for testing the Tau Command Line Tool. It is designed to test the functionality of the Tau tool and to ensure that it behaves as expected.

TODO swap python server with go mock server

## Running the tests

To run the tests, simply navigate to the root of the Tau project and run `$ go test ./...`. The tests will automatically be run and their output will be displayed in the terminal.

You can also pass the -v flag to the go test command to see more detailed output of the tests.

## Test Cases

The test cases are defined in the tests package and are organized into separate files for each command of the Tau tool. Each test case is defined as a struct of type testMonkey and includes fields such as the name of the test, the arguments to be passed to the test, the expected output, the environment variables to be set, and the expected exit code.

## Utilities

The utils_test.go file contains utility functions for running the tests. These include functions for validating the properties of a test case, building the tau command, creating temporary directories for the test, running the tests in parallel, and handling errors.

## Debugging

TODO `asd air` command needs updated flags for --ignore=tests/_fakeroot,tau,... and --root=../ then we can use it.  Otherwise the tests will run in a loop.

The framework also includes a debug flag that can be set on individual test cases. When this flag is set, the test will be run in a special "debug" mode where the stdout and stderr of the test will be printed to the terminal. This can be useful for troubleshooting failing tests.

Also of note if you have asd installed and you want to quickly test and debug a single test you can use the following commands:


TODO outdated, look at [main readme](../README.md###Hot_reload_Spider_tests) for updated instructions
```bash
$ cd tests

# Rebuilds the tau command and runs the test anytime a file in the tests directory changes
$ asd air <test_name>

# Or for no rebuild 
$ asd air <test_name> -tags no_rebuild
```

## Test Tags

Test tags are used to control which tests are run when the go test command is executed. The Tau Test Framework includes a `no_rebuild` tag that can be used to control whether the tau command is rebuilt before running the tests.

When the `no_rebuild` tag is used, the tau command will only be rebuilt if it is not found. This can be useful if you have already built the tau command and do not want to rebuild it every time you run the tests.

`$ go test -tags no_rebuild`
