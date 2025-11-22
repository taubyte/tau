load("utils.star", "add")

def score(target, data):
    # Use imported function in scoring
    length = len(target)
    bonus = add(length, 5)
    return float(bonus) * 0.1

def check(target, data):
    return len(target) > 0

