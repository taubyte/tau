# Cloud Development Kit

To re-build cdk/js/core.wasm, run:
```
cd plugin/
go run .
```

# Idea
The cdk/js (many other languages later - this is why we're using extism!) for example will expose the cloned config repository to the wasm module so it can be manipulated: creating funtions, storage, etc.