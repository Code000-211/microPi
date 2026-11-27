package docsearch

import (
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"unicode/utf8"
	"unsafe"
)

const (
	indexMagic  = "DSINDEX\x02"
	indexFormat = uint32(3)
	emptySlot   = ^uint32(0)
	headerSize  = 56 // 8 (magic) + 4 (format) + 4 (flags) + 8 (buildMs) + 8 (avgLen) + 4 (totalLen) + 4 (nf) + 4 (ns) + 4 (ne) + 4 (nt) + 4 (pad)
)

// ErrBadFile indicates a corrupt or incompatible index file.
var ErrBadFile = errors.New("docsearch: corrupt or incompatible index file")

// hash64 computes a deterministic 64-bit Murmur3-finalized hash over b.
func hash64(b []byte) uint64 {
	var h uint64 = 0x9e3779b97f4a7c15 ^ uint64(len(b))
	for _, c := range b {
		h ^= uint64(c)
		h *= 0x100000001b3
	}
	h ^= h >> 33
	h *= 0xff51afd7ed558ccd
	h ^= h >> 33
	h *= 0xc4ceb9fe1a85ec53
	h ^= h >> 33
	return h
}

func align8(n int) int {
	return (n + 7) &^ 7
}

// Strs stores packed UTF-8 strings with uint32 offsets (len = count + 1).
type Strs struct {
	Offsets []uint32
	Data    []byte
}

func (s *Strs) Len() int {
	if len(s.Offsets) <= 1 {
		return 0
	}
	return len(s.Offsets) - 1
}

func (s *Strs) Get(id uint32) string {
	idx := int(id)
	if idx < 0 || idx+1 >= len(s.Offsets) {
		return ""
	}
	start := s.Offsets[idx]
	end := s.Offsets[idx+1]
	if end < start || int(end) > len(s.Data) {
		return ""
	}
	return string(s.Data[start:end])
}

func (s *Strs) Bytes(id uint32) []byte {
	idx := int(id)
	if idx < 0 || idx+1 >= len(s.Offsets) {
		return nil
	}
	start := s.Offsets[idx]
	end := s.Offsets[idx+1]
	if end < start || int(end) > len(s.Data) {
		return nil
	}
	return s.Data[start:end]
}

// StrsBuilder builds a Strs table in memory.
type StrsBuilder struct {
	offsets []uint32
	data    []byte
}

func NewStrsBuilder(capItems int) *StrsBuilder {
	offsets := make([]uint32, 1, capItems+1)
	offsets[0] = 0
	return &StrsBuilder{offsets: offsets}
}

func (b *StrsBuilder) Add(s string) uint32 {
	id := uint32(len(b.offsets) - 1)
	b.data = append(b.data, s...)
	b.offsets = append(b.offsets, uint32(len(b.data)))
	return id
}

func (b *StrsBuilder) Len() int {
	return len(b.offsets) - 1
}

func (b *StrsBuilder) Build() Strs {
	return Strs{Offsets: b.offsets, Data: b.data}
}

// Lists stores CSR adjacency/posting lists with uint32 offsets (len = count + 1).
type Lists struct {
	Offsets []uint32
	Values  []uint32
}

func (l *Lists) Len() int {
	if len(l.Offsets) <= 1 {
		return 0
	}
	return len(l.Offsets) - 1
}

func (l *Lists) Get(id uint32) []uint32 {
	idx := int(id)
	if idx < 0 || idx+1 >= len(l.Offsets) {
		return nil
	}
	start := l.Offsets[idx]
	end := l.Offsets[idx+1]
	if end < start || int(end) > len(l.Values) {
		return nil
	}
	return l.Values[start:end]
}

// StrMap is an open-addressing hash table mapping strings (stored in a Strs table or key table)
// to uint32 values, using 64-bit Murmur3-finalized hashing.
type StrMap struct {
	Slots []uint32 // power-of-two size; emptySlot if unoccupied
	// For key lookup when keys are separate from values:
	Keys   Strs     // optional: if Keys.Len() > 0, slot stores index into Keys and Vals[slot] stores value
	Vals   []uint32 // parallel to Slots if Keys.Len() > 0; otherwise slot stores ID into external Strs
	Target *Strs    // used when Keys.Len() == 0 (slot directly stores ID into Target)
}

func (m *StrMap) Lookup(key string) (uint32, bool) {
	if len(m.Slots) == 0 {
		return 0, false
	}
	mask := uint64(len(m.Slots) - 1)
	pos := hash64([]byte(key)) & mask
	// At most one probe per slot: a table with no empty slot (a corrupt file) ends the search instead of looping forever.
	for range len(m.Slots) {
		slot := m.Slots[pos]
		if slot == emptySlot {
			return 0, false
		}
		if m.Keys.Len() > 0 {
			if string(m.Keys.Bytes(slot)) == key {
				return m.Vals[pos], true
			}
		} else if m.Target != nil {
			if string(m.Target.Bytes(slot)) == key {
				return slot, true
			}
		}
		pos = (pos + 1) & mask
	}
	return 0, false
}

func buildStrMapDirect(strs *Strs) StrMap {
	n := strs.Len()
	capSlots := 8
	for capSlots < n*2 {
		capSlots <<= 1
	}
	slots := make([]uint32, capSlots)
	for i := range slots {
		slots[i] = emptySlot
	}
	mask := uint64(capSlots - 1)
	for id := range uint32(n) {
		b := strs.Bytes(id)
		pos := hash64(b) & mask
		for slots[pos] != emptySlot {
			pos = (pos + 1) & mask
		}
		slots[pos] = id
	}
	return StrMap{Slots: slots, Target: strs}
}

func buildStrMapKV(pairs []kvPair) StrMap {
	n := len(pairs)
	capSlots := 8
	for capSlots < n*2 {
		capSlots <<= 1
	}
	slots := make([]uint32, capSlots)
	vals := make([]uint32, capSlots)
	for i := range slots {
		slots[i] = emptySlot
	}
	kb := NewStrsBuilder(n)
	mask := uint64(capSlots - 1)
	for _, kv := range pairs {
		kid := kb.Add(kv.key)
		pos := hash64([]byte(kv.key)) & mask
		for slots[pos] != emptySlot {
			pos = (pos + 1) & mask
		}
		slots[pos] = kid
		vals[pos] = kv.val
	}
	return StrMap{Slots: slots, Keys: kb.Build(), Vals: vals}
}

type kvPair struct {
	key string
	val uint32
}

// storeWriter writes 8-byte aligned blocks to a file atomically via temp file + rename.
type storeWriter struct {
	buf []byte
}

func newStoreWriter(capBytes int) *storeWriter {
	buf := make([]byte, headerSize, max(headerSize, capBytes))
	return &storeWriter{buf: buf}
}

func (w *storeWriter) writeBlock(raw []byte) {
	var lenBuf [8]byte
	binary.LittleEndian.PutUint64(lenBuf[:], uint64(len(raw)))
	w.buf = append(w.buf, lenBuf[:]...)
	w.buf = append(w.buf, raw...)
	pad := align8(len(raw)) - len(raw)
	for range pad {
		w.buf = append(w.buf, 0)
	}
}

func (w *storeWriter) writeUint32s(v []uint32) {
	if len(v) == 0 {
		w.writeBlock(nil)
		return
	}
	b := unsafe.Slice((*byte)(unsafe.Pointer(&v[0])), len(v)*4)
	w.writeBlock(b)
}

func (w *storeWriter) writeFloat32s(v []float32) {
	if len(v) == 0 {
		w.writeBlock(nil)
		return
	}
	b := unsafe.Slice((*byte)(unsafe.Pointer(&v[0])), len(v)*4)
	w.writeBlock(b)
}

func (w *storeWriter) writeInt64s(v []int64) {
	if len(v) == 0 {
		w.writeBlock(nil)
		return
	}
	b := unsafe.Slice((*byte)(unsafe.Pointer(&v[0])), len(v)*8)
	w.writeBlock(b)
}

func (w *storeWriter) writeBoolBytes(v []bool) {
	if len(v) == 0 {
		w.writeBlock(nil)
		return
	}
	b := make([]byte, len(v))
	for i, flag := range v {
		if flag {
			b[i] = 1
		}
	}
	w.writeBlock(b)
}

func (w *storeWriter) writeStrs(s Strs) {
	w.writeUint32s(s.Offsets)
	w.writeBlock(s.Data)
}

func (w *storeWriter) writeLists(l Lists) {
	w.writeUint32s(l.Offsets)
	w.writeUint32s(l.Values)
}

func (w *storeWriter) setHeader(buildMs uint64, avgLen float64, totalLen, nf, ns, ne, nt uint32) {
	copy(w.buf[0:8], indexMagic)
	binary.LittleEndian.PutUint32(w.buf[8:12], indexFormat)
	binary.LittleEndian.PutUint32(w.buf[12:16], 0)
	binary.LittleEndian.PutUint64(w.buf[16:24], buildMs)
	binary.LittleEndian.PutUint64(w.buf[24:32], math.Float64bits(avgLen))
	binary.LittleEndian.PutUint32(w.buf[32:36], totalLen)
	binary.LittleEndian.PutUint32(w.buf[36:40], nf)
	binary.LittleEndian.PutUint32(w.buf[40:44], ns)
	binary.LittleEndian.PutUint32(w.buf[44:48], ne)
	binary.LittleEndian.PutUint32(w.buf[48:52], nt)
	binary.LittleEndian.PutUint32(w.buf[52:56], 0)
}

type storeReader struct {
	data []byte
	pos  int
}

func newStoreReader(data []byte) (*storeReader, uint64, float64, uint32, uint32, uint32, uint32, uint32, error) {
	if len(data) < headerSize {
		return nil, 0, 0, 0, 0, 0, 0, 0, ErrBadFile
	}
	if string(data[0:8]) != indexMagic {
		return nil, 0, 0, 0, 0, 0, 0, 0, ErrBadFile
	}
	format := binary.LittleEndian.Uint32(data[8:12])
	if format != indexFormat {
		return nil, 0, 0, 0, 0, 0, 0, 0, ErrBadFile
	}
	buildMs := binary.LittleEndian.Uint64(data[16:24])
	avgLen := math.Float64frombits(binary.LittleEndian.Uint64(data[24:32]))
	totalLen := binary.LittleEndian.Uint32(data[32:36])
	nf := binary.LittleEndian.Uint32(data[36:40])
	ns := binary.LittleEndian.Uint32(data[40:44])
	ne := binary.LittleEndian.Uint32(data[44:48])
	nt := binary.LittleEndian.Uint32(data[48:52])
	return &storeReader{data: data, pos: headerSize}, buildMs, avgLen, totalLen, nf, ns, ne, nt, nil
}

func (r *storeReader) readBlock() ([]byte, error) {
	if r.pos+8 > len(r.data) {
		return nil, ErrBadFile
	}
	rawLen := binary.LittleEndian.Uint64(r.data[r.pos : r.pos+8])
	r.pos += 8
	rem := len(r.data) - r.pos
	if rawLen > uint64(rem) {
		return nil, ErrBadFile
	}
	byteLen := int(rawLen)
	aligned := align8(byteLen)
	if aligned < byteLen || aligned > rem {
		return nil, ErrBadFile
	}
	block := r.data[r.pos : r.pos+byteLen]
	r.pos += aligned
	return block, nil
}

func (r *storeReader) readUint32s() ([]uint32, error) {
	b, err := r.readBlock()
	if err != nil {
		return nil, err
	}
	if len(b)%4 != 0 {
		return nil, ErrBadFile
	}
	if len(b) == 0 {
		return nil, nil
	}
	return unsafe.Slice((*uint32)(unsafe.Pointer(&b[0])), len(b)/4), nil
}

func (r *storeReader) readFloat32s() ([]float32, error) {
	b, err := r.readBlock()
	if err != nil {
		return nil, err
	}
	if len(b)%4 != 0 {
		return nil, ErrBadFile
	}
	if len(b) == 0 {
		return nil, nil
	}
	return unsafe.Slice((*float32)(unsafe.Pointer(&b[0])), len(b)/4), nil
}

func (r *storeReader) readInt64s() ([]int64, error) {
	b, err := r.readBlock()
	if err != nil {
		return nil, err
	}
	if len(b)%8 != 0 {
		return nil, ErrBadFile
	}
	if len(b) == 0 {
		return nil, nil
	}
	return unsafe.Slice((*int64)(unsafe.Pointer(&b[0])), len(b)/8), nil
}

func (r *storeReader) readBoolBytes() ([]bool, error) {
	b, err := r.readBlock()
	if err != nil {
		return nil, err
	}
	out := make([]bool, len(b))
	for i, v := range b {
		out[i] = v != 0
	}
	return out, nil
}

func (r *storeReader) readStrs() (Strs, error) {
	offsets, err := r.readUint32s()
	if err != nil {
		return Strs{}, err
	}
	data, err := r.readBlock()
	if err != nil {
		return Strs{}, err
	}
	return Strs{Offsets: offsets, Data: data}, nil
}

func (r *storeReader) readLists() (Lists, error) {
	offsets, err := r.readUint32s()
	if err != nil {
		return Lists{}, err
	}
	values, err := r.readUint32s()
	if err != nil {
		return Lists{}, err
	}
	return Lists{Offsets: offsets, Values: values}, nil
}

func validateStrs(s Strs, expected int) error {
	if len(s.Offsets) != expected+1 {
		return fmt.Errorf("%w: strs offset len %d != %d", ErrBadFile, len(s.Offsets), expected+1)
	}
	prev := uint32(0)
	for _, off := range s.Offsets {
		if off < prev || int(off) > len(s.Data) {
			return fmt.Errorf("%w: invalid strs offset %d", ErrBadFile, off)
		}
		prev = off
	}
	if !utf8.Valid(s.Data) {
		return fmt.Errorf("%w: invalid utf8 in strs", ErrBadFile)
	}
	return nil
}

func validateLists(l Lists, expected int, maxVal uint32) error {
	if len(l.Offsets) != expected+1 {
		return fmt.Errorf("%w: lists offset len %d != %d", ErrBadFile, len(l.Offsets), expected+1)
	}
	prev := uint32(0)
	for _, off := range l.Offsets {
		if off < prev || int(off) > len(l.Values) {
			return fmt.Errorf("%w: invalid lists offset %d", ErrBadFile, off)
		}
		prev = off
	}
	for _, v := range l.Values {
		if v >= maxVal {
			return fmt.Errorf("%w: list value %d >= %d", ErrBadFile, v, maxVal)
		}
	}
	return nil
}

// validatePostings checks that offsets start at 0, never decrease and end at len(secs), and that every section id is below ns.
func validatePostings(offsets, secs []uint32, ns uint32) error {
	if len(offsets) == 0 || offsets[0] != 0 || int(offsets[len(offsets)-1]) != len(secs) {
		return fmt.Errorf("%w: posting offsets do not span the postings", ErrBadFile)
	}
	for i := 1; i < len(offsets); i++ {
		if offsets[i] < offsets[i-1] {
			return fmt.Errorf("%w: posting offsets decrease", ErrBadFile)
		}
	}
	for _, sec := range secs {
		if sec >= ns {
			return fmt.Errorf("%w: posting section out of range", ErrBadFile)
		}
	}
	return nil
}

func validateStrMap(m StrMap, maxTarget uint32) error {
	n := len(m.Slots)
	if n == 0 || (n&(n-1)) != 0 {
		return fmt.Errorf("%w: map slots not power of two: %d", ErrBadFile, n)
	}
	if !slices.Contains(m.Slots, emptySlot) {
		return fmt.Errorf("%w: map has no empty slot", ErrBadFile)
	}
	if m.Keys.Len() > 0 {
		if len(m.Vals) != n {
			return fmt.Errorf("%w: map vals len mismatch", ErrBadFile)
		}
		if err := validateStrs(m.Keys, m.Keys.Len()); err != nil {
			return err
		}
		for i, s := range m.Slots {
			if s != emptySlot && (int(s) >= m.Keys.Len() || m.Vals[i] >= maxTarget) {
				return fmt.Errorf("%w: map key slot or value out of range", ErrBadFile)
			}
		}
	} else {
		for _, s := range m.Slots {
			if s != emptySlot && s >= maxTarget {
				return fmt.Errorf("%w: map target slot out of range", ErrBadFile)
			}
		}
	}
	return nil
}

func okStampMatches(idxPath string, dev, ino uint64, size int64) bool {
	b, err := os.ReadFile(idxPath + ".ok")
	if err != nil || len(b) != 24 {
		return false
	}
	d := binary.LittleEndian.Uint64(b[0:8])
	i := binary.LittleEndian.Uint64(b[8:16])
	s := int64(binary.LittleEndian.Uint64(b[16:24]))
	return d == dev && i == ino && s == size
}

func writeOkStamp(idxPath string, dev, ino uint64, size int64) {
	var b [24]byte
	binary.LittleEndian.PutUint64(b[0:8], dev)
	binary.LittleEndian.PutUint64(b[8:16], ino)
	binary.LittleEndian.PutUint64(b[16:24], uint64(size))
	tmp := fmt.Sprintf("%s.ok.tmp.%d", idxPath, os.Getpid())
	if err := os.WriteFile(tmp, b[:], 0o644); err != nil {
		return
	}
	if err := os.Rename(tmp, idxPath+".ok"); err != nil {
		_ = os.Remove(tmp)
	}
}

// Save writes the index to idxPath atomically via a temporary file + rename.
func (idx *Index) Save(idxPath string) error {
	if err := os.MkdirAll(filepath.Dir(idxPath), 0o755); err != nil {
		return err
	}
	w := newStoreWriter(1 << 20)
	w.setHeader(idx.BuildMs, idx.AvgLen, idx.TotalLen, uint32(idx.NumFiles()), uint32(idx.NumSections()), uint32(idx.NumEntities()), uint32(idx.NumTerms()))

	w.writeStrs(idx.Files)
	w.writeInt64s(idx.FileMtimes)
	w.writeInt64s(idx.FileSizes)
	w.writeBoolBytes(idx.FileIsCode)

	w.writeUint32s(idx.SecFile)
	w.writeUint32s(idx.SecStart)
	w.writeUint32s(idx.SecEnd)
	w.writeUint32s(idx.SecLen)
	w.writeStrs(idx.SecHeadings)
	w.writeLists(idx.SecHeadTerms)
	w.writeLists(idx.SecEntities)

	w.writeStrs(idx.EntityNames)
	w.writeUint32s(idx.EntityMap.Slots)
	w.writeStrs(idx.EntityMap.Keys)
	w.writeUint32s(idx.EntityMap.Vals)
	w.writeLists(idx.EntitySecs)
	w.writeBoolBytes(idx.EntityIsHub)

	w.writeStrs(idx.TermNames)
	w.writeUint32s(idx.TermMap.Slots)
	w.writeUint32s(idx.TermDF)
	w.writeUint32s(idx.PostOffsets)
	w.writeUint32s(idx.PostSecs)
	w.writeFloat32s(idx.PostWeights)

	w.writeLists(idx.Graph)

	w.writeUint32s(idx.DefMap.Slots)
	w.writeStrs(idx.DefMap.Keys)
	w.writeUint32s(idx.DefMap.Vals)

	tmp := fmt.Sprintf("%s.tmp.%d", idxPath, os.Getpid())
	if err := os.WriteFile(tmp, w.buf, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, idxPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if fi, err := os.Stat(idxPath); err == nil {
		dev, ino, sz := fileIdentity(fi)
		writeOkStamp(idxPath, dev, ino, sz)
	}
	return nil
}

// Load opens idxPath via mmap (or read fallback), validates (using .ok stamp fast path),
// and returns a ready-to-search Index.
func Load(idxPath string, roots []string) (*Index, error) {
	f, err := os.Open(idxPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := fi.Size()
	if size < headerSize || size > int64(int(^uint(0)>>1)) {
		return nil, ErrBadFile
	}
	dev, ino, sz := fileIdentity(fi)
	fastOK := okStampMatches(idxPath, dev, ino, sz)

	data, unmapFn, err := mmapFile(f, int(size))
	if err != nil {
		return nil, err
	}

	idx, err := decodeIndex(data, roots, !fastOK)
	if err != nil {
		_ = unmapFn()
		return nil, err
	}
	idx.unmap = unmapFn
	runtime.SetFinalizer(idx, func(x *Index) { _ = x.Close() })
	if !fastOK {
		writeOkStamp(idxPath, dev, ino, sz)
	}
	return idx, nil
}

func decodeIndex(data []byte, roots []string, fullValidate bool) (*Index, error) {
	r, buildMs, avgLen, totalLen, nf, ns, ne, nt, err := newStoreReader(data)
	if err != nil {
		return nil, err
	}
	idx := &Index{
		Roots:    append([]string(nil), roots...),
		BuildMs:  buildMs,
		AvgLen:   avgLen,
		TotalLen: totalLen,
	}

	if idx.Files, err = r.readStrs(); err != nil {
		return nil, err
	}
	if idx.FileMtimes, err = r.readInt64s(); err != nil {
		return nil, err
	}
	if idx.FileSizes, err = r.readInt64s(); err != nil {
		return nil, err
	}
	if idx.FileIsCode, err = r.readBoolBytes(); err != nil {
		return nil, err
	}

	if idx.SecFile, err = r.readUint32s(); err != nil {
		return nil, err
	}
	if idx.SecStart, err = r.readUint32s(); err != nil {
		return nil, err
	}
	if idx.SecEnd, err = r.readUint32s(); err != nil {
		return nil, err
	}
	if idx.SecLen, err = r.readUint32s(); err != nil {
		return nil, err
	}
	if idx.SecHeadings, err = r.readStrs(); err != nil {
		return nil, err
	}
	if idx.SecHeadTerms, err = r.readLists(); err != nil {
		return nil, err
	}
	if idx.SecEntities, err = r.readLists(); err != nil {
		return nil, err
	}

	if idx.EntityNames, err = r.readStrs(); err != nil {
		return nil, err
	}
	if idx.EntityMap.Slots, err = r.readUint32s(); err != nil {
		return nil, err
	}
	if idx.EntityMap.Keys, err = r.readStrs(); err != nil {
		return nil, err
	}
	if idx.EntityMap.Vals, err = r.readUint32s(); err != nil {
		return nil, err
	}
	if idx.EntitySecs, err = r.readLists(); err != nil {
		return nil, err
	}
	if idx.EntityIsHub, err = r.readBoolBytes(); err != nil {
		return nil, err
	}

	if idx.TermNames, err = r.readStrs(); err != nil {
		return nil, err
	}
	if idx.TermMap.Slots, err = r.readUint32s(); err != nil {
		return nil, err
	}
	idx.TermMap.Target = &idx.TermNames
	if idx.TermDF, err = r.readUint32s(); err != nil {
		return nil, err
	}
	if idx.PostOffsets, err = r.readUint32s(); err != nil {
		return nil, err
	}
	if idx.PostSecs, err = r.readUint32s(); err != nil {
		return nil, err
	}
	if idx.PostWeights, err = r.readFloat32s(); err != nil {
		return nil, err
	}

	if idx.Graph, err = r.readLists(); err != nil {
		return nil, err
	}

	if idx.DefMap.Slots, err = r.readUint32s(); err != nil {
		return nil, err
	}
	if idx.DefMap.Keys, err = r.readStrs(); err != nil {
		return nil, err
	}
	if idx.DefMap.Vals, err = r.readUint32s(); err != nil {
		return nil, err
	}

	if fullValidate {
		if err := validateStrs(idx.Files, int(nf)); err != nil {
			return nil, err
		}
		if len(idx.FileMtimes) != int(nf) || len(idx.FileSizes) != int(nf) || len(idx.FileIsCode) != int(nf) {
			return nil, ErrBadFile
		}
		if len(idx.SecFile) != int(ns) || len(idx.SecStart) != int(ns) || len(idx.SecEnd) != int(ns) || len(idx.SecLen) != int(ns) {
			return nil, ErrBadFile
		}
		if err := validateStrs(idx.SecHeadings, int(ns)); err != nil {
			return nil, err
		}
		if err := validateLists(idx.SecHeadTerms, int(ns), max(nt, 1)); err != nil {
			return nil, err
		}
		if err := validateLists(idx.SecEntities, int(ns), max(ne, 1)); err != nil {
			return nil, err
		}
		if err := validateStrs(idx.EntityNames, int(ne)); err != nil {
			return nil, err
		}
		if err := validateStrMap(idx.EntityMap, max(ne, 1)); err != nil {
			return nil, err
		}
		if err := validateLists(idx.EntitySecs, int(ne), max(ns, 1)); err != nil {
			return nil, err
		}
		if len(idx.EntityIsHub) != int(ne) {
			return nil, ErrBadFile
		}
		if err := validateStrs(idx.TermNames, int(nt)); err != nil {
			return nil, err
		}
		if err := validateStrMap(idx.TermMap, max(nt, 1)); err != nil {
			return nil, err
		}
		if len(idx.TermDF) != int(nt) || len(idx.PostOffsets) != int(nt)+1 || len(idx.PostSecs) != len(idx.PostWeights) {
			return nil, ErrBadFile
		}
		if err := validatePostings(idx.PostOffsets, idx.PostSecs, ns); err != nil {
			return nil, err
		}
		if err := validateLists(idx.Graph, int(ns+ne), max(ns+ne, 1)); err != nil {
			return nil, err
		}
		if err := validateStrMap(idx.DefMap, max(ns, 1)); err != nil {
			return nil, err
		}
	}

	return idx, nil
}
