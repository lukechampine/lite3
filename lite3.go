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
	LITE3_NODE_TYPE_SHIFT = 0
	LITE3_NODE_TYPE_MASK  = ((1 << 8) - 1) // 8 LSB

	LITE3_NODE_GEN_SHIFT = 8
	LITE3_NODE_GEN_MASK  = (^((1 << 8) - 1)) // 24 MSB

	LITE3_NODE_KEY_COUNT_MAX = 7
	LITE3_NODE_KEY_COUNT_MIN = (LITE3_NODE_KEY_COUNT_MAX / 2)

	LITE3_NODE_KEY_COUNT_SHIFT = 0
	LITE3_NODE_KEY_COUNT_MASK  = ((1 << 3) - 1) // 3 LSB	key_count: 0-7          hashes[7]       kv_offs[7]       child_offs[8]	LITE3_NODE_SIZE: 96 (1.5 cache lines)

	LITE3_KEY_TAG_SIZE_MIN   = 1
	LITE3_KEY_TAG_SIZE_MAX   = 4
	LITE3_KEY_TAG_SIZE_MASK  = ((1 << 2) - 1)
	LITE3_KEY_TAG_SIZE_SHIFT = 0

	LITE3_KEY_TAG_KEY_SIZE_MASK  = (^((1 << 2) - 1))
	LITE3_KEY_TAG_KEY_SIZE_SHIFT = 2

	LITE3_NODE_SIZE           = 96
	LITE3_TREE_HEIGHT_MAX     = 9 // key_count: 0-7       LITE3_NODE_SIZE: 96 (1.5 cache lines)
	LITE3_NODE_SIZE_KC_OFFSET = 32
	LITE3_NODE_SIZE_SHIFT     = 6
	LITE3_NODE_SIZE_MASK      = (^((1 << 6) - 1)) // 26 MSB

	LITE3_HASH_PROBE_MAX = 128

	LITE3_VAL_SIZE = 1

	LITE3_NODE_ALIGNMENT      = 4
	LITE3_NODE_ALIGNMENT_MASK = (LITE3_NODE_ALIGNMENT - 1)
)

const (
	LITE3_TYPE_NULL    = iota //  maps to 'null' type in JSON
	LITE3_TYPE_BOOL           //  maps to 'boolean' type in JSON; underlying datatype: `bool`
	LITE3_TYPE_I64            //  maps to 'number' type in JSON; underlying datatype: `int64`
	LITE3_TYPE_F64            //  maps to 'number' type in JSON; underlying datatype: `float64`
	LITE3_TYPE_BYTES          //  coverted to base64 string in JSON
	LITE3_TYPE_STRING         //  maps to 'string' type in JSON
	LITE3_TYPE_OBJECT         //  maps to 'object' type in JSON
	LITE3_TYPE_ARRAY          //  maps to 'array' type in JSON
	LITE3_TYPE_INVALID        //  any type value equal or greater than this is considered invalid
	LITE3_TYPE_COUNT          //  not an actual type, only used for counting
)

var lite3_type_sizes = [...]int{
	LITE3_TYPE_NULL:    0,
	LITE3_TYPE_BOOL:    1,
	LITE3_TYPE_I64:     8,
	LITE3_TYPE_F64:     8,
	LITE3_TYPE_BYTES:   4,
	LITE3_TYPE_STRING:  4,
	LITE3_TYPE_OBJECT:  LITE3_NODE_SIZE - LITE3_VAL_SIZE, // `type` field is contained inside node->gen_type
	LITE3_TYPE_ARRAY:   LITE3_NODE_SIZE - LITE3_VAL_SIZE, // `type` field is contained inside node->gen_type
	LITE3_TYPE_INVALID: 0,
}

var errCollision = errors.New("key hash collision")

type Buffer struct {
	buf []byte
}

func (b *Buffer) SetNull(key string) error {
	if err := _verify_obj_set(b.buf, 0); err != nil {
		return err
	}
	_, err := b.set(0, key, keyHash(key), LITE3_TYPE_NULL, 0)
	return err
}

func (b *Buffer) IsNull(key string) (bool, error) {
	if err := _verify_obj_get(b.buf, 0); err != nil {
		return false, err
	}
	_, err := b.get(0, key, keyHash(key), LITE3_TYPE_NULL)
	return err == nil, err
}

func (b *Buffer) SetBool(key string, val bool) error {
	if err := _verify_obj_set(b.buf, 0); err != nil {
		return err
	}
	buf, err := b.set(0, key, keyHash(key), LITE3_TYPE_BOOL, 1)
	if err != nil {
		return err
	}
	if val {
		buf[0] = 1
	} else {
		buf[0] = 0
	}
	return nil
}

func (b *Buffer) GetBool(key string) (bool, error) {
	if err := _verify_obj_get(b.buf, 0); err != nil {
		return false, err
	}
	buf, err := b.get(0, key, keyHash(key), LITE3_TYPE_BOOL)
	if err != nil {
		return false, err
	}
	return buf[0] != 0, nil
}

func (b *Buffer) SetInt(key string, val int) error {
	return b.setInt(0, key, LITE3_TYPE_I64, uint64(val))
}

func (b *Buffer) GetInt(key string) (int, error) {
	u, err := b.getInt(key, LITE3_TYPE_I64)
	return int(u), err
}

func (b *Buffer) SetFloat64(key string, val float64) error {
	return b.setInt(0, key, LITE3_TYPE_F64, math.Float64bits(val))
}

func (b *Buffer) GetFloat64(key string) (float64, error) {
	u, err := b.getInt(key, LITE3_TYPE_F64)
	return math.Float64frombits(u), err
}

func (b *Buffer) SetBytes(key string, val []byte) error {
	return b.setBytes(0, key, LITE3_TYPE_BYTES, val)
}

func (b *Buffer) GetBytes(key string) ([]byte, error) {
	buf, err := b.GetRawBytes(key)
	return slices.Clone(buf), err
}

func (b *Buffer) GetRawBytes(key string) ([]byte, error) {
	buf, err := b.getBytes(key, LITE3_TYPE_BYTES)
	return buf, err
}

func (b *Buffer) SetString(key string, val string) error {
	return b.setBytes(0, key, LITE3_TYPE_STRING, unsafe.Slice(unsafe.StringData(val), len(val)))
}

func (b *Buffer) GetString(key string) (string, error) {
	buf, err := b.getBytes(key, LITE3_TYPE_STRING)
	if err != nil {
		return "", err
	}
	return string(buf), nil
}

func (b *Buffer) GetRawString(key string) (string, error) {
	buf, err := b.getBytes(key, LITE3_TYPE_STRING)
	if err != nil {
		return "", err
	}
	return unsafe.String(&buf[0], len(buf)), nil
}

func (b *Buffer) MarshalJSON() ([]byte, error) {
	panic("TODO")
}

func New() *Buffer {
	b := &Buffer{buf: make([]byte, LITE3_NODE_SIZE, LITE3_NODE_SIZE+2048)} // TODO: growable buffer
	b.initObject(0)                                                        // TODO: not always object
	return b
}

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

func keyHash(key string) uint32 {
	hash := uint32(5381) // LITE3_DJB2_HASH_SEED
	for i := range key {
		hash = ((hash << 5) + hash) + uint32(key[i])
	}
	return hash
}

type entry struct {
	typ int
	val []byte
}

func (e entry) AsBool() bool       { return e.val[0] != 0 }
func (e entry) AsUint64() uint64   { return binary.LittleEndian.Uint64(e.val) }
func (e entry) AsInt() int         { return int(e.AsUint64()) }
func (e entry) AsFloat64() float64 { return math.Float64frombits(e.AsUint64()) }
func (e entry) AsBytes() []byte    { return e.val }
func (e entry) AsString() string   { return string(e.val) }

func (b *Buffer) initObject(off int) {
	*cast[treeNode](b.buf, off) = treeNode{gen_type: LITE3_TYPE_OBJECT & LITE3_NODE_TYPE_MASK}
}

func (b *Buffer) initArray(off int) {
	*cast[treeNode](b.buf, off) = treeNode{gen_type: LITE3_TYPE_ARRAY & LITE3_NODE_TYPE_MASK}
}

// Verifies a key inside the buffer to ensure readers don't go out of bounds.
// Optionally, compares the existing key to an input key; a mismatch implies a
// hash collision.
func _verify_key(buf []byte, key string, key_tag_size int, offs *int) (out_key_tag_size int, err error) {
	if len(buf) < LITE3_KEY_TAG_SIZE_MAX {
		return 0, errors.New("key entry out of bounds")
	}
	_key_tag_size := int((buf[*offs] & LITE3_KEY_TAG_SIZE_MASK) + 1)
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
	if typ == LITE3_TYPE_INVALID {
		return 0, errors.New("value type invalid")
	}
	_val_entry_size := LITE3_VAL_SIZE + lite3_type_sizes[typ]

	if _val_entry_size > len(buf) || off > len(buf)-_val_entry_size {
		return 0, errors.New("value out of bounds")
	}
	if typ == LITE3_TYPE_STRING || typ == LITE3_TYPE_BYTES { // extra check required for str/bytes
		byte_count := *cast[uint32](buf, off+LITE3_VAL_SIZE)
		_val_entry_size += int(byte_count)
		if _val_entry_size > len(buf) || off > len(buf)-_val_entry_size {
			return 0, errors.New("value out of bounds")
		}
	}
	return off + _val_entry_size, nil
}

func _verify_get(buf []byte, off int) error {
	if len(buf) < off+LITE3_NODE_SIZE {
		return errors.New("node out of bounds")
	}
	return nil
}

func _verify_obj_get(buf []byte, off int) error {
	if err := _verify_get(buf, off); err != nil {
		return err
	} else if *cast[uint8](buf, off) != LITE3_TYPE_OBJECT {
		return errors.New("expected object type")
	}
	return nil
}

func _verify_set(buf []byte, off int) error {
	if cap(buf) < off+LITE3_NODE_SIZE {
		return errors.New("node out of bounds")
	}
	return nil
}

func _verify_obj_set(buf []byte, off int) error {
	if err := _verify_set(buf, off); err != nil {
		return err
	} else if *cast[uint8](buf, off) != LITE3_TYPE_OBJECT {
		return errors.New("expected object type")
	}
	return nil
}

func (b *Buffer) setInt(off int, key string, typ uint8, value uint64) error {
	if err := _verify_obj_set(b.buf, off); err != nil {
		return err
	}
	val, err := b.set(off, key, keyHash(key), typ, 8)
	if err != nil {
		return err
	}
	binary.LittleEndian.PutUint64(val, value)
	return nil
}

func (b *Buffer) setBytes(off int, key string, typ uint8, value []byte) error {
	if err := _verify_obj_set(b.buf, off); err != nil {
		return err
	}
	buf, err := b.set(off, key, keyHash(key), typ, 4+len(value)+1)
	if err != nil {
		return err
	}
	binary.LittleEndian.PutUint32(buf, uint32(len(value)+1))
	copy(buf[4:], value)
	buf[4+len(value)] = 0 // C string terminator
	return nil
}

func (b *Buffer) set(off int, key string, keyHash uint32, val_typ uint8, val_len int) ([]byte, error) {
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
	gen := (root.gen_type >> LITE3_NODE_GEN_SHIFT) + 1
	root.gen_type = (root.gen_type & ^LITE3_NODE_GEN_MASK) | (gen << LITE3_NODE_GEN_SHIFT)

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
			if node.size_kc&LITE3_NODE_KEY_COUNT_MASK == LITE3_NODE_KEY_COUNT_MAX { // node full, need to split
				buflen_aligned := (len(b.buf) + LITE3_NODE_ALIGNMENT_MASK) & ^LITE3_NODE_ALIGNMENT_MASK // next multiple of LITE3_NODE_ALIGNMENT
				new_node_size := LITE3_NODE_SIZE
				if parent != nil {
					new_node_size = 2 * LITE3_NODE_SIZE
				}
				if new_node_size > cap(b.buf)-len(b.buf) || buflen_aligned > cap(b.buf)-len(b.buf)-new_node_size {
					// TODO: grow buffer instead
					return nil, errors.New("no buffer space for node split")
				}
				b.buf = b.buf[:buflen_aligned]
				if parent == nil {
					// if root split, create new root
					*cast[treeNode](b.buf, buflen_aligned) = *node
					node = cast[treeNode](b.buf, buflen_aligned)
					parent = cast[treeNode](b.buf, off)
					*parent = treeNode{
						gen_type: parent.gen_type,
						size_kc:  parent.size_kc &^ LITE3_NODE_KEY_COUNT_MASK,
					}
					parent.child_offs[0] = uint32(len(b.buf))
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
				parent.child_offs[i+1] = uint32(len(b.buf))                                                                                 // insert sibling as child in parent
				parent.size_kc = (parent.size_kc & ^uint32(LITE3_NODE_KEY_COUNT_MASK)) | ((parent.size_kc + 1) & LITE3_NODE_KEY_COUNT_MASK) // key_count++

				node.hashes[LITE3_NODE_KEY_COUNT_MIN] = 0
				node.kv_offs[LITE3_NODE_KEY_COUNT_MIN] = 0
				sibling := cast[treeNode](b.buf, len(b.buf))
				*sibling = treeNode{
					gen_type: node.gen_type & LITE3_NODE_TYPE_MASK,
					size_kc:  LITE3_NODE_KEY_COUNT_MIN & LITE3_NODE_KEY_COUNT_MASK,
				}
				node.size_kc = LITE3_NODE_KEY_COUNT_MIN & LITE3_NODE_KEY_COUNT_MASK
				sibling.child_offs[0] = node.child_offs[LITE3_NODE_KEY_COUNT_MIN+1] // take child from node
				node.child_offs[LITE3_NODE_KEY_COUNT_MIN+1] = 0
				for j := 0; j < LITE3_NODE_KEY_COUNT_MIN; j++ { // copy half of node's keys to sibling
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
				key_count = int(node.size_kc & LITE3_NODE_KEY_COUNT_MASK)
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
						return nil, err
					}
				}
				val_start_offs := target_offs
				verify, err := _verify_val(b.buf, target_offs)
				if err != nil {
					return nil, err
				}
				if val_len >= verify-val_start_offs { // value is too large, we must append
					alignment_mask := 0
					unaligned_val_offs := len(b.buf) + key_tag_size + len(key)
					alignment_padding := ((unaligned_val_offs + alignment_mask) & ^alignment_mask) - unaligned_val_offs
					entry_size += alignment_padding
					if entry_size > cap(b.buf)-len(b.buf) {
						// TODO: grow buffer instead
						return nil, errors.New("no buffer space for entry insertion")
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
				//entry := cast[entry](b.buf, val_start_offs)
				panic("TODO")
			}

			if node.child_offs[0] != 0 {
				next_node_offs := int(node.child_offs[i])
				parent = node
				node = cast[treeNode](b.buf, next_node_offs)
				if node_walks++; node_walks > LITE3_TREE_HEIGHT_MAX {
					return nil, errors.New("node walks exceeded LITE3_TREE_HEIGHT_MAX")
				}
			} else {
				// insert the kv-pair
				alignment_mask := 0
				unaligned_val_offs := len(b.buf) + key_tag_size + len(key)
				alignment_padding := ((unaligned_val_offs + alignment_mask) & ^alignment_mask) - unaligned_val_offs
				entry_size += alignment_padding
				if entry_size > cap(b.buf)-len(b.buf) {
					// TODO: grow buffer instead
					return nil, errors.New("no buffer space for entry insertion")
				}
				for j := key_count; j > i; j-- {
					node.hashes[j] = node.hashes[j-1]
					node.kv_offs[j] = node.kv_offs[j-1]
				}
				node.hashes[i] = attempt_key_hash
				node.size_kc = (node.size_kc & ^uint32(LITE3_NODE_KEY_COUNT_MASK)) | ((node.size_kc + 1) & LITE3_NODE_KEY_COUNT_MASK) // key_count++
				b.buf = b.buf[:len(b.buf)+alignment_padding]
				node.kv_offs[i] = uint32(len(b.buf))
				root = cast[treeNode](b.buf, off)
				size := (root.size_kc >> LITE3_NODE_SIZE_SHIFT) + 1
				root.size_kc = (root.size_kc & ^LITE3_NODE_SIZE_MASK) | (size << LITE3_NODE_SIZE_SHIFT) // node size++
				break outer
			}
		}

		if attempt++; attempt == probe_attempts {
			return nil, errors.New("LITE3_HASH_PROBE_MAX limit reached")
		}
	}

	if key != "" {
		key_size_tmp := ((len(key) + 1) << LITE3_KEY_TAG_KEY_SIZE_SHIFT) | (key_tag_size - 1)
		b.buf = binary.LittleEndian.AppendUint32(b.buf, uint32(key_size_tmp))
		b.buf = b.buf[:len(b.buf)-4+key_tag_size]
		b.buf = append(b.buf, []byte(key)...)
		b.buf = append(b.buf, 0) // C string terminator
	}
	b.buf = append(b.buf, val_typ)
	b.buf = b.buf[:len(b.buf)+val_len]
	return b.buf[len(b.buf)-val_len:], nil
}

func (b *Buffer) getInt(key string, typ uint8) (uint64, error) {
	if err := _verify_obj_get(b.buf, 0); err != nil {
		return 0, err
	}
	val, err := b.get(0, key, keyHash(key), typ)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(val), nil
}

func (b *Buffer) getBytes(key string, typ uint8) ([]byte, error) {
	if err := _verify_obj_get(b.buf, 0); err != nil {
		return nil, err
	}
	buf, err := b.get(0, key, keyHash(key), typ)
	if err != nil {
		return nil, err
	}
	strlen := binary.LittleEndian.Uint32(buf[:4]) - 1 // C string terminator
	return buf[4:][:strlen], nil
}

func (b *Buffer) get(off int, key string, keyHash uint32, val_typ uint8) ([]byte, error) {
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
			key_count := int(node.size_kc & LITE3_NODE_KEY_COUNT_MASK)
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
						return nil, err
					}
				}
				val_start_offs := target_offs
				if _, err := _verify_val(b.buf, target_offs); err != nil {
					return nil, err
				}
				if b.buf[val_start_offs] != val_typ {
					return nil, errors.New("value type mismatch")
				}
				return b.buf[val_start_offs+1:], nil
			}
			if node.child_offs[0] == 0 {
				return nil, errors.New("key not found")
			}
			next_node_offs := int(node.child_offs[i])
			node = cast[treeNode](b.buf, next_node_offs)

			if node_walks++; node_walks > LITE3_TREE_HEIGHT_MAX {
				return nil, errors.New("node walks exceeded LITE3_TREE_HEIGHT_MAX")
			}
		}
	}
	return nil, errors.New("LITE3_HASH_PROBE_MAX limit reached")
}

type iter struct {
	b         *Buffer
	gen       uint32
	node_offs [LITE3_TREE_HEIGHT_MAX + 1]uint32
	depth     int
	node_i    [LITE3_TREE_HEIGHT_MAX + 1]int
}

func (b *Buffer) Iter(off int) (*iter, error) {
	if err := _verify_get(b.buf, off); err != nil {
		return nil, err
	}

	node := cast[treeNode](b.buf, off)
	typ := node.gen_type & LITE3_NODE_TYPE_MASK
	if !(typ == LITE3_TYPE_OBJECT || typ == LITE3_TYPE_ARRAY) {
		return nil, errors.New("not an array or object type")
	}
	it := &iter{
		b:   b,
		gen: node.gen_type,
	}
	it.node_offs[0] = uint32(off)

	for node.child_offs[0] != 0 { // has children, travel down
		next_node_offs := node.child_offs[0]
		node = cast[treeNode](b.buf, int(next_node_offs))
		if it.depth++; it.depth > LITE3_TREE_HEIGHT_MAX {
			return nil, errors.New("node walks exceeded LITE3_TREE_HEIGHT_MAX")
		}
		it.node_offs[it.depth] = next_node_offs
		it.node_i[it.depth] = 0
	}
	return it, nil
}

func (it *iter) Next() (key string, typ uint8, val []byte, err error) {
	if it.gen != cast[treeNode](it.b.buf, int(it.node_offs[0])).gen_type {
		return "", 0, nil, errors.New("buffer mutated")
	}
	node := cast[treeNode](it.b.buf, int(it.node_offs[it.depth]))
	nodeTyp := node.gen_type & LITE3_NODE_TYPE_MASK
	if !(nodeTyp == LITE3_TYPE_OBJECT || nodeTyp == LITE3_TYPE_ARRAY) {
		return "", 0, nil, errors.New("not an array or object type")
	}
	if it.depth == 0 && (it.node_i[it.depth] == int(node.size_kc&LITE3_NODE_KEY_COUNT_MASK)) { // key_count reached, done
		return "", 0, nil, io.EOF
	}

	target_offs := int(node.kv_offs[it.node_i[it.depth]])

	if nodeTyp == LITE3_TYPE_OBJECT { // write back key if not NULL
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
	for it.depth > 0 && it.node_i[it.depth] == int(node.size_kc&LITE3_NODE_KEY_COUNT_MASK) {
		it.depth--
		node = cast[treeNode](it.b.buf, int(it.node_offs[it.depth]))
	}
	return key, typ, val, nil
}

func (b *Buffer) SetObject(key string) error {
	if err := _verify_obj_set(b.buf, 0); err != nil {
		return err
	}
	buf, err := b.set(0, key, keyHash(key), LITE3_TYPE_OBJECT, lite3_type_sizes[LITE3_TYPE_OBJECT])
	if err != nil {
		return err
	}
	b.initObject(len(b.buf) - len(buf))
	return nil
}

func (b *Buffer) SetArray(key string) error {
	if err := _verify_obj_set(b.buf, 0); err != nil {
		return err
	}
	buf, err := b.set(0, key, keyHash(key), LITE3_TYPE_ARRAY, lite3_type_sizes[LITE3_TYPE_ARRAY])
	if err != nil {
		return err
	}
	b.initArray(len(b.buf) - len(buf))
	return nil
}

func (b *Buffer) AppendObject() error {
	keyHash := (cast[treeNode](b.buf, 0).size_kc >> LITE3_NODE_SIZE_SHIFT)
	buf, err := b.set(0, "", keyHash, LITE3_TYPE_OBJECT, lite3_type_sizes[LITE3_TYPE_OBJECT])
	if err != nil {
		return err
	}
	b.initObject(len(b.buf) - len(buf))
	return nil
}

func (b *Buffer) AppendArray() error {
	keyHash := (cast[treeNode](b.buf, 0).size_kc >> LITE3_NODE_SIZE_SHIFT)
	buf, err := b.set(0, "", keyHash, LITE3_TYPE_ARRAY, lite3_type_sizes[LITE3_TYPE_ARRAY])
	if err != nil {
		return err
	}
	b.initArray(len(b.buf) - len(buf))
	return nil
}

func (b *Buffer) SetArrayObject(index uint32) error {
	keyHash := index
	buf, err := b.set(0, "", keyHash, LITE3_TYPE_OBJECT, lite3_type_sizes[LITE3_TYPE_OBJECT])
	if err != nil {
		return err
	}
	b.initObject(len(b.buf) - len(buf))
	return nil
}

func (b *Buffer) SetArrayArray(index uint32) error {
	keyHash := index
	buf, err := b.set(0, "", keyHash, LITE3_TYPE_ARRAY, lite3_type_sizes[LITE3_TYPE_ARRAY])
	if err != nil {
		return err
	}
	b.initArray(len(b.buf) - len(buf))
	return nil
}
