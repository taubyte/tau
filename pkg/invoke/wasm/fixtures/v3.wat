;; The smallest handler that needs WebAssembly 3.0: the global its response
;; address comes from is initialised by an extended constant expression, the
;; feature componentize-py's CPython needs. It answers 200 "ok" to anything.
;;
;; wasm-tools parse v3.wat -o v3.wasm
(component
  (core module $m
    (memory (export "memory") 1)
    (global $response i32 (i32.add (i32.const 8) (i32.const 8)))
    (global $bump (mut i32) (i32.const 1024))
    ;; at 16 the response: status 200, no headers, and the body at 64, 2 bytes
    (data (i32.const 16) "\c8\00\00\00" "\00\00\00\00" "\00\00\00\00" "\40\00\00\00" "\02\00\00\00")
    (data (i32.const 64) "ok")
    (func (export "cabi_realloc") (param i32 i32 i32 i32) (result i32)
      (local $at i32)
      (local.set $at
        (i32.and
          (i32.add (global.get $bump) (i32.sub (local.get 2) (i32.const 1)))
          (i32.sub (i32.const 0) (local.get 2))))
      (global.set $bump (i32.add (local.get $at) (local.get 3)))
      (local.get $at))
    (func (export "handle") (param i32 i32 i32 i32 i32 i32 i32 i32) (result i32)
      (global.get $response)))
  (core instance $i (instantiate $m))
  (type $header' (record (field "name" string) (field "value" string)))
  (export $header "header" (type $header'))
  (type $request' (record (field "method" string) (field "path" string) (field "headers" (list $header)) (field "body" (list u8))))
  (export $request "request" (type $request'))
  (type $response' (record (field "status" u16) (field "headers" (list $header)) (field "body" (list u8))))
  (export $response "response" (type $response'))
  (func $handle (param "req" $request) (result $response)
    (canon lift (core func $i "handle") (memory $i "memory") (realloc (func $i "cabi_realloc"))))
  (export "handle" (func $handle)))
