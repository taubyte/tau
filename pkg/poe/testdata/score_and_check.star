def score(target, data):
    # Score based on target and data (normalized to [0, 1])
    base_score = float(len(target)) / 10.0
    if data and "multiplier" in data:
        base_score = base_score * float(data["multiplier"]) / 2.0
    # Clamp to [0, 1]
    return min(1.0, max(0.0, base_score))

def check(target, data):
    # Check if target is valid and data has required fields
    if not target or len(target) == 0:
        return False
    if data and "required" in data:
        return bool(data["required"])
    return True

