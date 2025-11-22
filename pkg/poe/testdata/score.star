def score(target, data):
    # Simple scoring logic: return a score based on target length (normalized to [0, 1])
    length = len(target)
    # Normalize: divide by 10 and clamp to [0, 1]
    normalized = min(1.0, float(length) / 10.0)
    return normalized

