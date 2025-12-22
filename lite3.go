// Package lite3 is an implementation of the Lite³ serialization format.
package lite3

import (
	"encoding/binary"
	"errors"
	"io"
	"math"
	"slices"
	"unsafe"
)

const (
	LITE3_NODE_TYPE_MASK = ((1 << 8) - 1) // 8 LSB

	LITE3_NODE_GEN_SHIFT = 8
	LITE3_NODE_GEN_MASK  = (^((1 << 8) - 1)) // 24 MSB

	LITE3_NODE_KEY_COUNT_MAX = 7
	LITE3_NODE_KEY_COUNT_MIN = (LITE3_NODE_KEY_COUNT_MAX / 2)

	LITE3_KEY_TAG_KEY_SIZE_SHIFT = 2

	LITE3_NODE_SIZE       = 96
	LITE3_TREE_HEIGHT_MAX = 9 // key_count: 0-7       LITE3_NODE_SIZE: 96 (1.5 cache lines)

	LITE3_HASH_PROBE_MAX = 128

	LITE3_VAL_SIZE = 1

	LITE3_NODE_ALIGNMENT      = 4
	LITE3_NODE_ALIGNMENT_MASK = (LITE3_NODE_ALIGNMENT - 1)
)

const (
	TypeNull = iota
	TypeBool
	TypeInt
	TypeFloat
	TypeBytes
	TypeString
	TypeObject
	TypeArray
	TypeInvalid
)

var typeSizes = [...]int{
	TypeNull:    0,
	TypeBool:    1,
	TypeInt:     8,
	TypeFloat:   8,
	TypeBytes:   4,
	TypeString:  4,
	TypeObject:  LITE3_NODE_SIZE - LITE3_VAL_SIZE, // `type` field is contained inside node->gen_type
	TypeArray:   LITE3_NODE_SIZE - LITE3_VAL_SIZE, // `type` field is contained inside node->gen_type
	TypeInvalid: 0,
}

var errCollision = errors.New("hash collision")

type Buffer struct {
	buf []byte
}

func (b *Buffer) setRootContainer(typ uint8) *container {
	if len(b.buf) < LITE3_NODE_SIZE {
		b.buf = b.buf[:LITE3_NODE_SIZE]
	}
	n := cast[treeNode](b.buf, 0)
	*n = treeNode{gen_type: uint32(typ)}
	return &container{
		b:    b,
		typ:  typ,
		off:  0,
		node: n,
	}
}

func (b *Buffer) SetRootObject() *Object { return &Object{b.setRootContainer(TypeObject)} }
func (b *Buffer) SetRootArray() *Array   { return &Array{b.setRootContainer(TypeArray)} }

func (b *Buffer) Bytes() []byte { return b.buf }

func New(buf []byte) *Buffer {
	return &Buffer{buf: buf}
}

type Object struct {
	c *container
}

func (o *Object) Count() int                         { return o.c.count() }
func (o *Object) Iter() *Iter                        { return o.c.iter() }
func (o *Object) SetNull(key string)                 { o.c.setNull(key, keyHash(key)) }
func (o *Object) SetBool(key string, val bool)       { o.c.setBool(key, keyHash(key), val) }
func (o *Object) Bool(key string) bool               { return o.c.bool(key, keyHash(key)) }
func (o *Object) SetInt(key string, val int)         { o.c.setInt(key, keyHash(key), val) }
func (o *Object) Int(key string) int                 { return o.c.int(key, keyHash(key)) }
func (o *Object) SetFloat64(key string, val float64) { o.c.setFloat64(key, keyHash(key), val) }
func (o *Object) Float64(key string) float64         { return o.c.float64(key, keyHash(key)) }
func (o *Object) SetBytes(key string, val []byte)    { o.c.setBytes(key, keyHash(key), val) }
func (o *Object) Bytes(key string) []byte            { return o.c.bytes(key, keyHash(key)) }
func (o *Object) RawBytes(key string) []byte         { return o.c.rawBytes(key, keyHash(key)) }
func (o *Object) SetString(key string, val string)   { o.c.setString(key, keyHash(key), val) }
func (o *Object) String(key string) string           { return o.c.string(key, keyHash(key)) }
func (o *Object) RawString(key string) string        { return o.c.rawString(key, keyHash(key)) }
func (o *Object) SetObject(key string) *Object       { return o.c.setObject(key, keyHash(key)) }
func (o *Object) Object(key string) *Object          { return o.c.object(key, keyHash(key)) }
func (o *Object) SetArray(key string) *Array         { return o.c.setArray(key, keyHash(key)) }
func (o *Object) Array(key string) *Array            { return o.c.array(key, keyHash(key)) }

type Array struct {
	c *container
}

func (a *Array) Count() int                    { return a.c.count() }
func (a *Array) Iter() *Iter                   { return a.c.iter() }
func (a *Array) SetNull(i int)                 { a.c.setNull("", uint32(i)) }
func (a *Array) SetBool(i int, val bool)       { a.c.setBool("", uint32(i), val) }
func (a *Array) Bool(i int) bool               { return a.c.bool("", uint32(i)) }
func (a *Array) SetInt(i int, val int)         { a.c.setInt("", uint32(i), val) }
func (a *Array) Int(i int) int                 { return a.c.int("", uint32(i)) }
func (a *Array) SetFloat64(i int, val float64) { a.c.setFloat64("", uint32(i), val) }
func (a *Array) Float64(i int) float64         { return a.c.float64("", uint32(i)) }
func (a *Array) SetBytes(i int, val []byte)    { a.c.setBytes("", uint32(i), val) }
func (a *Array) Bytes(i int) []byte            { return a.c.bytes("", uint32(i)) }
func (a *Array) RawBytes(i int) []byte         { return a.c.rawBytes("", uint32(i)) }
func (a *Array) SetString(i int, val string)   { a.c.setString("", uint32(i), val) }
func (a *Array) String(i int) string           { return a.c.string("", uint32(i)) }
func (a *Array) RawString(i int) string        { return a.c.rawString("", uint32(i)) }
func (a *Array) SetObject(i int) *Object       { return a.c.setObject("", uint32(i)) }
func (a *Array) Object(i int) *Object          { return a.c.object("", uint32(i)) }
func (a *Array) SetArray(i int) *Array         { return a.c.setArray("", uint32(i)) }
func (a *Array) Array(i int) *Array            { return a.c.array("", uint32(i)) }

////

func cast[T any](buf []byte, off int) *T {
	return (*T)(unsafe.Pointer(uintptr(unsafe.Pointer(unsafe.SliceData(buf))) + uintptr(off)))
}

type treeNode struct {
	gen_type   uint32
	hashes     [7]uint32
	size_kc    uint32
	kv_offs    [7]uint32
	child_offs [8]uint32
}

func (node *treeNode) gen() uint32 {
	return node.gen_type >> 8
}

func (node *treeNode) setGen(gen uint32) {
	node.gen_type &= (1 << 8) - 1
	node.gen_type |= gen << 8
}

func (node *treeNode) Type() uint8 {
	return uint8(node.gen_type)
}

func (node *treeNode) setType(typ uint8) {
	node.gen_type &^= 255
	node.gen_type |= uint32(typ)
}

func (node *treeNode) size() int {
	return int(node.size_kc >> 6)
}

func (node *treeNode) setSize(size int) {
	node.size_kc &= (1 << 6) - 1
	node.size_kc |= uint32(size) << 6
}

func (node *treeNode) keyCount() int {
	return int(node.size_kc & 7)
}

func (node *treeNode) setKeyCount(kc int) {
	node.size_kc &^= 7
	node.size_kc |= uint32(kc) & 7
}

func keyHash(key string) uint32 {
	hash := uint32(5381) // LITE3_DJB2_HASH_SEED
	for i := range key {
		hash = ((hash << 5) + hash) + uint32(key[i])
	}
	return hash
}

type container struct {
	b    *Buffer
	typ  uint8
	off  int // necessary?
	node *treeNode
}

func (c *container) count() int { return c.node.size() }

func (c *container) setContainer(key string, keyHash uint32, typ uint8) *container {
	off := c.b.set(c.off, key, keyHash, TypeObject, typeSizes[TypeObject])
	n := cast[treeNode](c.b.buf, off)
	*n = treeNode{gen_type: uint32(typ)}
	return &container{
		b:    c.b,
		typ:  typ,
		off:  off,
		node: n,
	}
}

func (c *container) container(key string, keyHash uint32, exp uint8) *container {
	typ, off := c.b.get(c.off, key, keyHash)
	if typ != exp {
		return nil
	}
	return &container{
		b:    c.b,
		typ:  typ,
		off:  off,
		node: cast[treeNode](c.b.buf, off),
	}
}

func (c *container) setNull(key string, keyHash uint32) {
	c.b.set(c.off, key, keyHash, TypeNull, 0)
}

func (c *container) setBool(key string, keyHash uint32, val bool) {
	off := c.b.set(c.off, key, keyHash, TypeBool, 1)
	if val {
		c.b.buf[off] = 1
	} else {
		c.b.buf[off] = 0
	}
}

func (c *container) bool(key string, keyHash uint32) bool {
	typ, off := c.b.get(c.off, key, keyHash)
	buf := c.b.buf[off:]
	return typ == TypeBool && len(buf) > 0 && buf[0] != 0
}

func (c *container) setUint64(key string, keyHash uint32, typ uint8, val uint64) {
	buf := c.b.buf[c.b.set(c.off, key, keyHash, typ, 8):]
	binary.LittleEndian.PutUint64(buf, val)
}

func (c *container) number(key string, keyHash uint32, exp uint8) uint64 {
	typ, off := c.b.get(c.off, key, keyHash)
	buf := c.b.buf[off:]
	if len(buf) >= 8 {
		return binary.LittleEndian.Uint64(buf)
	} else if typ != exp {
		return 0
	}
	return 0
}

func (c *container) setInt(key string, keyHash uint32, val int) {
	c.setUint64(key, keyHash, TypeInt, uint64(val))
}

func (c *container) int(key string, keyHash uint32) int {
	return int(c.number(key, keyHash, TypeInt))
}

func (c *container) setFloat64(key string, keyHash uint32, val float64) {
	c.setUint64(key, keyHash, TypeFloat, math.Float64bits(val))
}

func (c *container) float64(key string, keyHash uint32) float64 {
	return math.Float64frombits(c.number(key, keyHash, TypeFloat))
}

func (c *container) setTypedBytes(key string, keyHash uint32, typ uint8, val []byte) {
	buf := c.b.buf[c.b.set(c.off, key, keyHash, typ, 4+len(val)+1):]
	binary.LittleEndian.PutUint32(buf, uint32(len(val)+1))
	copy(buf[4:], val)
	buf[4+len(val)] = 0 // C string terminator
}

func (c *container) typedBytes(key string, keyHash uint32, exp uint8) []byte {
	typ, off := c.b.get(c.off, key, keyHash)
	buf := c.b.buf[off:]
	if len(buf) < 4 {
		return nil
	} else if typ != exp {
		return nil
	}
	strlen := binary.LittleEndian.Uint32(buf[:4]) - 1 // C string terminator
	if len(buf[4:]) < int(strlen) {
		return nil
	}
	return buf[4:][:strlen]
}

func (c *container) setBytes(key string, keyHash uint32, val []byte) {
	c.setTypedBytes(key, keyHash, TypeBytes, val)
}

func (c *container) bytes(key string, keyHash uint32) []byte {
	return slices.Clone(c.rawBytes(key, keyHash))
}

func (c *container) rawBytes(key string, keyHash uint32) []byte {
	return c.typedBytes(key, keyHash, TypeBytes)
}

func (c *container) setString(key string, keyHash uint32, val string) {
	c.setTypedBytes(key, keyHash, TypeString, unsafe.Slice(unsafe.StringData(val), len(val)))
}

func (c *container) string(key string, keyHash uint32) string {
	return string(c.typedBytes(key, keyHash, TypeString))
}

func (c *container) rawString(key string, keyHash uint32) string {
	b := c.typedBytes(key, keyHash, TypeString)
	return unsafe.String(&b[0], len(b))
}

func (c *container) setObject(key string, keyHash uint32) *Object {
	return &Object{c.setContainer(key, keyHash, TypeObject)}
}

func (c *container) object(key string, keyHash uint32) *Object {
	return &Object{c.container(key, keyHash, TypeObject)}
}

func (c *container) setArray(key string, keyHash uint32) *Array {
	return &Array{c.setContainer(key, keyHash, TypeArray)}
}

func (c *container) array(key string, keyHash uint32) *Array {
	return &Array{c.container(key, keyHash, TypeArray)}
}

//////

// Verifies a key inside the buffer to ensure readers don't go out of bounds.
// Optionally, compares the existing key to an input key; a mismatch implies a
// hash collision.
func _verify_key(buf []byte, key string, key_tag_size int, offs *int) (out_key_tag_size int, err error) {
	const maxTagSize = 4
	if len(buf) < maxTagSize {
		return 0, errors.New("key entry out of bounds")
	}
	_key_tag_size := int((buf[*offs] & (maxTagSize - 1)) + 1)
	if key_tag_size != 0 && key_tag_size != _key_tag_size {
		return 0, errors.New("key tag size does not match")
	}
	_key_size := (*cast[uint32](buf, *offs) & ((1 << 8 * uint32(_key_tag_size)) - 1)) >> LITE3_KEY_TAG_KEY_SIZE_SHIFT
	*offs += _key_tag_size
	if len(buf) < int(_key_size)+*offs {
		return 0, errors.New("key entry out of bounds")
	}
	if key != "" && key != string(buf[*offs:][:_key_size-1]) {
		return 0, errCollision
	}
	*offs += int(_key_size)
	if out_key_tag_size != 0 {
		out_key_tag_size = _key_tag_size
	}
	return out_key_tag_size, nil
}

func _verify_val(buf []byte, off int) (int, error) {
	if len(buf) < LITE3_VAL_SIZE || off > len(buf)-LITE3_VAL_SIZE {
		return 0, errors.New("value out of bounds")
	}
	typ := *cast[uint8](buf, off)
	if typ == TypeInvalid {
		return 0, errors.New("value type invalid")
	}
	_val_entry_size := LITE3_VAL_SIZE + typeSizes[typ]

	if _val_entry_size > len(buf) || off > len(buf)-_val_entry_size {
		return 0, errors.New("value out of bounds")
	}
	if typ == TypeString || typ == TypeBytes { // extra check required for str/bytes
		byte_count := *cast[uint32](buf, off+LITE3_VAL_SIZE)
		_val_entry_size += int(byte_count)
		if _val_entry_size > len(buf) || off > len(buf)-_val_entry_size {
			return 0, errors.New("value out of bounds")
		}
	}
	return off + _val_entry_size, nil
}

func (b *Buffer) set(off int, key string, keyHash uint32, val_typ uint8, val_len int) int {
	key_tag_size := 0
	if ((len(key) >> (16 - LITE3_KEY_TAG_KEY_SIZE_SHIFT)) << 1) > 0 {
		key_tag_size++
	}
	if (len(key) >> (8 - LITE3_KEY_TAG_KEY_SIZE_SHIFT)) > 0 {
		key_tag_size++
	}
	if len(key) > 0 {
		key_tag_size++
	}
	base_entry_size := key_tag_size + len(key) + LITE3_VAL_SIZE + val_len

	root := cast[treeNode](b.buf, off)
	root.setGen(root.gen() + 1)

	probe_attempts := uint32(1)
	if key != "" {
		probe_attempts = LITE3_HASH_PROBE_MAX
	}

outer:
	for attempt := uint32(0); ; attempt++ {
		attempt_key_hash := keyHash + attempt*attempt

		entry_size := base_entry_size
		var parent *treeNode
		node := root

		var key_count, i int
		node_walks := 0
		for {
			match := false
			if node.keyCount() == LITE3_NODE_KEY_COUNT_MAX { // node full, need to split
				buflen_aligned := (len(b.buf) + LITE3_NODE_ALIGNMENT_MASK) & ^LITE3_NODE_ALIGNMENT_MASK // next multiple of LITE3_NODE_ALIGNMENT
				new_node_size := LITE3_NODE_SIZE
				if parent != nil {
					new_node_size = 2 * LITE3_NODE_SIZE
				}
				if new_node_size > cap(b.buf)-len(b.buf) || buflen_aligned > cap(b.buf)-len(b.buf)-new_node_size {
					// TODO: grow buffer instead
					panic(errors.New("no buffer space for node split"))
				}
				b.buf = b.buf[:buflen_aligned]
				if parent == nil {
					// if root split, create new root
					*cast[treeNode](b.buf, buflen_aligned) = *node
					node = cast[treeNode](b.buf, buflen_aligned)
					parent = cast[treeNode](b.buf, off)
					parent.setKeyCount(0)
					parent.hashes = [7]uint32{}
					parent.kv_offs = [7]uint32{}
					parent.child_offs = [8]uint32{0: uint32(len(b.buf))}
					b.buf = b.buf[:len(b.buf)+LITE3_NODE_SIZE]
					key_count = 0
					i = 0
				}
				for j := key_count; j > i; j-- { // shift parent array before separator insert
					parent.hashes[j] = parent.hashes[j-1]
					parent.kv_offs[j] = parent.kv_offs[j-1]
					parent.child_offs[j+1] = parent.child_offs[j]
				}
				parent.hashes[i] = node.hashes[LITE3_NODE_KEY_COUNT_MIN] // insert new separator key in parent
				parent.kv_offs[i] = node.kv_offs[LITE3_NODE_KEY_COUNT_MIN]
				parent.child_offs[i+1] = uint32(len(b.buf)) // insert sibling as child in parent
				parent.setKeyCount(parent.keyCount() + 1)

				node.hashes[LITE3_NODE_KEY_COUNT_MIN] = 0
				node.kv_offs[LITE3_NODE_KEY_COUNT_MIN] = 0
				sibling := cast[treeNode](b.buf, len(b.buf))
				*sibling = treeNode{
					gen_type: uint32(node.Type()),
					size_kc:  LITE3_NODE_KEY_COUNT_MIN,
				}
				node.size_kc = LITE3_NODE_KEY_COUNT_MIN
				sibling.child_offs[0] = node.child_offs[LITE3_NODE_KEY_COUNT_MIN+1] // take child from node
				node.child_offs[LITE3_NODE_KEY_COUNT_MIN+1] = 0
				for j := range LITE3_NODE_KEY_COUNT_MIN { // copy half of node's keys to sibling
					sibling.hashes[j] = node.hashes[j+LITE3_NODE_KEY_COUNT_MIN+1]
					sibling.kv_offs[j] = node.kv_offs[j+LITE3_NODE_KEY_COUNT_MIN+1]
					sibling.child_offs[j+1] = node.child_offs[j+LITE3_NODE_KEY_COUNT_MIN+2]
					node.hashes[j+LITE3_NODE_KEY_COUNT_MIN+1] = 0
					node.kv_offs[j+LITE3_NODE_KEY_COUNT_MIN+1] = 0
					node.child_offs[j+LITE3_NODE_KEY_COUNT_MIN+2] = 0
				}
				b.buf = b.buf[:len(b.buf)+LITE3_NODE_SIZE]
				if attempt_key_hash > parent.hashes[i] { // sibling has target key? then we follow
					node = sibling
				} else if attempt_key_hash == parent.hashes[i] {
					node = parent
					match = true
				}
			}

			if !match {
				key_count = node.keyCount()
				i = 0
				for i < key_count && node.hashes[i] < attempt_key_hash {
					i++
				}
				match = i < key_count && node.hashes[i] == attempt_key_hash
			}
			if match {
				target_offs := int(node.kv_offs[i])
				key_start_offs := target_offs
				if key != "" {
					if _, err := _verify_key(b.buf, key, key_tag_size, &target_offs); err == errCollision {
						break // try next probe
					} else if err != nil {
						panic(err)
					}
				}
				val_start_offs := target_offs
				verify, err := _verify_val(b.buf, target_offs)
				if err != nil {
					panic(err)
				}
				if val_len >= verify-val_start_offs { // value is too large, we must append
					alignment_mask := 0
					unaligned_val_offs := len(b.buf) + key_tag_size + len(key)
					alignment_padding := ((unaligned_val_offs + alignment_mask) & ^alignment_mask) - unaligned_val_offs
					entry_size += alignment_padding
					if entry_size > cap(b.buf)-len(b.buf) {
						// TODO: grow buffer instead
						panic(errors.New("no buffer space for entry insertion"))
					}
					for j := 0; j < target_offs-key_start_offs; j++ {
						b.buf[int(node.kv_offs[i])+j] = 0
					}
					b.buf = b.buf[:len(b.buf)+alignment_padding]
					node.kv_offs[i] = uint32(len(b.buf))
					break outer
				}
				for i := 0; i < target_offs-val_start_offs; i++ {
					b.buf[val_start_offs+i] = 0
				}
				return val_start_offs
			}

			if node.child_offs[0] != 0 {
				next_node_offs := int(node.child_offs[i])
				parent = node
				node = cast[treeNode](b.buf, next_node_offs)
				if node_walks++; node_walks > LITE3_TREE_HEIGHT_MAX {
					panic(errors.New("node walks exceeded LITE3_TREE_HEIGHT_MAX"))
				}
			} else {
				// insert the kv-pair
				alignment_mask := 0
				unaligned_val_offs := len(b.buf) + key_tag_size + len(key)
				alignment_padding := ((unaligned_val_offs + alignment_mask) & ^alignment_mask) - unaligned_val_offs
				entry_size += alignment_padding
				if entry_size > cap(b.buf)-len(b.buf) {
					// TODO: grow buffer instead
					panic(errors.New("no buffer space for entry insertion"))
				}
				for j := key_count; j > i; j-- {
					node.hashes[j] = node.hashes[j-1]
					node.kv_offs[j] = node.kv_offs[j-1]
				}
				node.hashes[i] = attempt_key_hash
				node.setKeyCount(node.keyCount() + 1)
				b.buf = b.buf[:len(b.buf)+alignment_padding]
				node.kv_offs[i] = uint32(len(b.buf))
				root = cast[treeNode](b.buf, off)
				root.setSize(root.size() + 1)
				break outer
			}
		}

		if attempt++; attempt == probe_attempts {
			panic(errors.New("LITE3_HASH_PROBE_MAX limit reached"))
		}
	}

	if key != "" {
		key_size_tmp := ((len(key) + 1) << LITE3_KEY_TAG_KEY_SIZE_SHIFT) | (key_tag_size - 1)
		b.buf = binary.LittleEndian.AppendUint32(b.buf, uint32(key_size_tmp))
		b.buf = b.buf[:len(b.buf)-4+key_tag_size]
		b.buf = append(b.buf, []byte(key)...) // TODO: copy instead
		b.buf = append(b.buf, 0)              // C string terminator
	}
	b.buf = append(b.buf, val_typ)
	b.buf = b.buf[:len(b.buf)+val_len]
	return len(b.buf) - val_len
}

func (b *Buffer) get(off int, key string, keyHash uint32) (uint8, int) {
	key_tag_size := 0
	if ((len(key) >> (16 - LITE3_KEY_TAG_KEY_SIZE_SHIFT)) << 1) > 0 {
		key_tag_size++
	}
	if (len(key) >> (8 - LITE3_KEY_TAG_KEY_SIZE_SHIFT)) > 0 {
		key_tag_size++
	}
	if len(key) > 0 {
		key_tag_size++
	}

	probe_attempts := uint32(1)
	if key != "" {
		probe_attempts = LITE3_HASH_PROBE_MAX
	}

	for attempt := uint32(0); attempt < probe_attempts; attempt++ {
		attempt_key_hash := keyHash + attempt*attempt

		node := cast[treeNode](b.buf, off)

		node_walks := 0
		for {
			key_count := node.keyCount()
			i := 0
			for i < key_count && node.hashes[i] < attempt_key_hash {
				i++
			}
			if i < key_count && node.hashes[i] == attempt_key_hash { // target key found
				target_offs := int(node.kv_offs[i])
				if key != "" {
					if _, err := _verify_key(b.buf, key, key_tag_size, &target_offs); err == errCollision {
						break // try next probe
					} else if err != nil {
						panic(err)
					}
				}
				val_start_offs := target_offs
				if _, err := _verify_val(b.buf, target_offs); err != nil {
					panic(err)
				}
				return b.buf[val_start_offs], val_start_offs + 1
			}
			if node.child_offs[0] == 0 {
				panic(errors.New("key not found: " + key))
			}
			next_node_offs := int(node.child_offs[i])
			node = cast[treeNode](b.buf, next_node_offs)

			if node_walks++; node_walks > LITE3_TREE_HEIGHT_MAX {
				panic(errors.New("node walks exceeded LITE3_TREE_HEIGHT_MAX"))
			}
		}
	}
	panic(errors.New("LITE3_HASH_PROBE_MAX limit reached"))
}

type Iter struct {
	b         *Buffer
	gen       uint32
	node_offs [LITE3_TREE_HEIGHT_MAX + 1]uint32
	depth     int
	node_i    [LITE3_TREE_HEIGHT_MAX + 1]int
}

func (c *container) iter() *Iter {
	node := c.node
	typ := node.Type()
	if !(typ == TypeObject || typ == TypeArray) {
		panic(errors.New("not an array or object type"))
	}
	it := &Iter{
		b:   c.b,
		gen: node.gen(),
	}
	it.node_offs[0] = uint32(c.off)

	for node.child_offs[0] != 0 { // has children, travel down
		next_node_offs := node.child_offs[0]
		node = cast[treeNode](c.b.buf, int(next_node_offs))
		if it.depth++; it.depth > LITE3_TREE_HEIGHT_MAX {
			panic(errors.New("node walks exceeded LITE3_TREE_HEIGHT_MAX"))
		}
		it.node_offs[it.depth] = next_node_offs
		it.node_i[it.depth] = 0
	}
	return it
}

func (it *Iter) Next() (key string, typ uint8, val []byte, err error) {
	if it.gen != cast[treeNode](it.b.buf, int(it.node_offs[0])).gen() {
		return "", 0, nil, errors.New("buffer mutated")
	}
	node := cast[treeNode](it.b.buf, int(it.node_offs[it.depth]))
	nodeTyp := node.Type()
	if !(nodeTyp == TypeObject || nodeTyp == TypeArray) {
		return "", 0, nil, errors.New("not an array or object type")
	}
	if it.depth == 0 && (it.node_i[it.depth] == node.keyCount()) { // key_count reached, done
		return "", 0, nil, io.EOF
	}

	target_offs := int(node.kv_offs[it.node_i[it.depth]])

	if nodeTyp == TypeObject { // write back key if not NULL
		key_start_offs := target_offs
		if _, err := _verify_key(it.b.buf, "", 0, &target_offs); err != nil {
			return "", 0, nil, err
		}
		key = string(it.b.buf[key_start_offs+1:][:(target_offs - key_start_offs - 2)]) // exclude C string terminator
	}
	val_start_offs := target_offs
	if _, err := _verify_val(it.b.buf, target_offs); err != nil {
		return "", 0, nil, err
	}
	typ, val = it.b.buf[val_start_offs], it.b.buf[val_start_offs+1:]
	it.node_i[it.depth]++

	for node.child_offs[it.node_i[it.depth]] != 0 { // has children, travel down
		next_node_offs := node.child_offs[it.node_i[it.depth]]
		node = cast[treeNode](it.b.buf, int(next_node_offs))
		if it.depth++; it.depth > LITE3_TREE_HEIGHT_MAX {
			return "", 0, nil, errors.New("node walks exceeded LITE3_TREE_HEIGHT_MAX")
		}
		it.node_offs[it.depth] = next_node_offs
		it.node_i[it.depth] = 0
	}
	for it.depth > 0 && it.node_i[it.depth] == node.keyCount() {
		it.depth--
		node = cast[treeNode](it.b.buf, int(it.node_offs[it.depth]))
	}
	return key, typ, val, nil
}
