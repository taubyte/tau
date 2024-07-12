load("test.star", "test")

def Add2(x,y):
    return test.add2(x, y)

def Div(x,y):
    return test.div(x, y)

def Hello():
    return test.hello()

def Concatenate(x,y):
    return test.concatenate(x, y)

def SumFloat(x,y):
    return test.sumFloat(x,y)

def And(x,y):
    return test.boolAnd(x,y)

def ListLength(l):
    return test.listLength(l)

def DictSize(d):
    return test.dictSize(d)

def Nothing():
    return test.nothing()
