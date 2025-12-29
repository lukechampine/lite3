lite3
-----

[![GoDoc](https://godoc.org/lukechampine.com/lite3?status.svg)](https://godoc.org/lukechampine.com/lite3)

```
go get lukechampine.com/lite3
```

This repo contains a pure-Go implementation of [Lite³](https://lite3.io), a
zero-copy binary serialization format. Lite³ represents data as a B-tree inside
a single contiguous buffer, allowing access and mutation on any arbitrary field
in `O(log n)` time.

## Usage

The API is fairly low-level. There are two container types: `Object` and
`Array`, which each have `Get` and `Set` methods for reading and writing `Value`
objects.

```go
import "lukechampine.com/lite3"
import "fmt"

func main() {
    b := lite3.New(nil)
    o := b.SetRootObject()
	o.Set("foo", lite3.Bool(true))
    fmt.Println(o.Value("foo").Bool()) // true
    
    // nested containers:
    a := o.SetArray("bar")
    a.Set(0, lite3.String("hello"))
    a.Set(1, lite3.String("world"))

    // iteration:
    for key, val := range o.All() {
        if val.Type == lite3.TypeArray {
            for i, val := range val.All() {
                fmt.Println(i, val) // 0 hello
                break
            }
        } else {
            fmt.Println(key, val) // foo true
        }
    }

    // json compatibility:
    js, _ := b.MarshalJSON()
    fmt.Println(string(js)) // {"bar":["hello","world"],"foo":true}
    
    var b2 lite3.Buffer
    b2.UnmarshalJSON(js)
    fmt.Println(b2.Root().(lite3.Object).Value("foo")) // true
}
```

## Performance

...is currently underwhelming, because this implementation skews more towards
explicit encoding/decoding than unsafe casting. Should be possible to make it
much faster by assuming little-endian architecture.
