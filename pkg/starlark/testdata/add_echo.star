load("test.star", "test")
load("printer.star", "printer")

def echo():
    return printer.echo(test.add(5,3))
