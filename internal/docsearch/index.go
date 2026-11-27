package docsearch

import (
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/procpolicy"
)

const (
	bm25K1           = 1.2
	bm25B            = 0.75
	bm25BCode        = 0.5
	headingBoost     = 2
	pprAlpha         = 0.5
	pprEps           = 1e-4
	pprMaxPushes     = 200_000
	topLexicalCap    = 300
	defaultMaxPerDoc = 3
)

// Config configures index building.
type Config struct {
	Roots        []string
	MaxFileBytes int64
	MaxFiles     int
	// Extensions are the extensions to index, lowercase without the dot; nil indexes every text file.
	Extensions []string
}

// SearchOpts configures a search query.
type SearchOpts struct {
	K          int
	PathPrefix string
	// AbsPathPrefix keeps the files under this absolute path (index.ts resolves the tool's `path` against the project).
	AbsPathPrefix string
	MaxPerFile    int
	MaxChars      int
	Graph         *bool // nil defaults to true
}

func (o SearchOpts) useGraph() bool {
	if o.Graph == nil {
		return true
	}
	return *o.Graph
}

// Hit represents one ranked search result.
type Hit struct {
	Path      string  `json:"path"`
	AbsPath   string  `json:"-"`
	LineStart uint32  `json:"lineStart"`
	LineEnd   uint32  `json:"lineEnd"`
	Heading   string  `json:"heading"`
	Score     float64 `json:"score"`
	Coverage  float64 `json:"coverage"`
	Named     int     `json:"named"`
	Text      string  `json:"text"`
	Truncated bool    `json:"truncated"`
}

// Results holds the output of a search query.
type Results struct {
	Hits     []Hit    `json:"hits"`
	Entities []string `json:"entities"`
	Micros   int64    `json:"micros"`
	QTerms   int      `json:"qTerms"`
}

// Index is an in-memory or mmap-backed search index.
type Index struct {
	Roots    []string
	BuildMs  uint64
	AvgLen   float64
	TotalLen uint32

	Files      Strs
	FileMtimes []int64
	FileSizes  []int64
	FileIsCode []bool

	SecFile      []uint32
	SecStart     []uint32
	SecEnd       []uint32
	SecLen       []uint32
	SecHeadings  Strs
	SecHeadTerms Lists
	SecEntities  Lists

	EntityNames Strs
	EntityMap   StrMap
	EntitySecs  Lists
	EntityIsHub []bool

	TermNames   Strs
	TermMap     StrMap
	TermDF      []uint32
	PostOffsets []uint32
	PostSecs    []uint32
	PostWeights []float32

	Graph Lists

	DefMap StrMap

	unmap func() error
}

// Close releases mmap resources if the index was loaded from disk.
func (idx *Index) Close() error {
	if idx == nil || idx.unmap == nil {
		return nil
	}
	fn := idx.unmap
	idx.unmap = nil
	return fn()
}

func (idx *Index) NumFiles() int    { return idx.Files.Len() }
func (idx *Index) NumSections() int { return len(idx.SecFile) }
func (idx *Index) NumEntities() int { return idx.EntityNames.Len() }
func (idx *Index) NumTerms() int    { return idx.TermNames.Len() }

// Overlay holds incremental updates on top of a base Index.
type Overlay struct {
	Base        *Index
	Index       *Index
	Excluded    []bool // len == Base.NumSections(), true if section's file was modified or deleted
	ChangedRels map[string]struct{}
	DeletedRels map[string]struct{}
	CreatedAt   time.Time
	FileCount   int
}

type parsedChunk struct {
	heading   string
	lineStart uint32
	lineEnd   uint32
	bodyTerms []string
	headTerms []string
	entities  []string
	defName   string
}

type parsedFile struct {
	meta   FileMeta
	isCode bool
	chunks []parsedChunk
}

func parseFileForIndex(meta FileMeta, maxBytes int64) (parsedFile, bool) {
	text, ok, err := readTextFile(meta.Abs, maxBytes)
	if err != nil || !ok {
		return parsedFile{}, false
	}
	chunks := SplitChunks(meta.Rel, text)
	if len(chunks) == 0 {
		return parsedFile{}, false
	}
	isCode := IsCodePath(meta.Rel)
	base := filepath.Base(meta.Rel)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	fileStemTerms := Terms(stem)

	pChunks := make([]parsedChunk, 0, len(chunks))
	for i, c := range chunks {
		bTerms := Terms(c.Text)
		hTerms := Terms(c.Heading)
		if len(fileStemTerms) > 0 {
			hTerms = append(hTerms, fileStemTerms...)
		}
		ents := Entities(c.Text, meta.Rel, i == 0)
		var defName string
		if isCode && c.Heading != "" {
			defName = extractDefSymbol(c.Heading)
		}
		pChunks = append(pChunks, parsedChunk{
			heading:   c.Heading,
			lineStart: c.LineStart,
			lineEnd:   c.LineEnd,
			bodyTerms: bTerms,
			headTerms: hTerms,
			entities:  ents,
			defName:   defName,
		})
	}
	return parsedFile{meta: meta, isCode: isCode, chunks: pChunks}, true
}

func extractDefSymbol(heading string) string {
	// Take the last segment after " › " if merged, then last "::" or "." segment.
	seg := heading
	if idx := strings.LastIndex(seg, " › "); idx >= 0 {
		seg = seg[idx+3:]
	}
	if idx := strings.LastIndex(seg, "::"); idx >= 0 {
		seg = seg[idx+2:]
	}
	if idx := strings.LastIndex(seg, "."); idx >= 0 {
		seg = seg[idx+1:]
	}
	seg = strings.TrimSpace(seg)
	if len(seg) < 3 {
		return ""
	}
	for i := 0; i < len(seg); i++ {
		c := seg[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_') {
			return ""
		}
	}
	return strings.ToLower(seg)
}

// Build scans cfg.Roots and builds a full Index in parallel.
func Build(cfg Config) (*Index, error) {
	defer procpolicy.Widen()()
	t0 := time.Now()
	maxBytes := cfg.MaxFileBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxFileBytes
	}
	maxFiles := cfg.MaxFiles
	if maxFiles <= 0 {
		maxFiles = DefaultMaxFiles
	}
	files, err := ScanExtensions(cfg.Roots, normalizeExtensions(cfg.Extensions), maxBytes, maxFiles)
	if err != nil {
		return nil, err
	}
	parsed := parseFilesParallel(files, maxBytes)
	idx := assembleIndex(cfg.Roots, parsed, nil, uint64(time.Since(t0).Milliseconds()))
	return idx, nil
}

func parseFilesParallel(files []FileMeta, maxBytes int64) []parsedFile {
	if len(files) == 0 {
		return nil
	}
	// µPi: the index is built in the background while the agent's own tools run; half the CPUs leave them the rest.
	workers := min(max(runtime.NumCPU()/2, 1), len(files))
	results := make([]parsedFile, len(files))
	valid := make([]bool, len(files))

	ch := make(chan int, len(files))
	for i := range files {
		ch <- i
	}
	close(ch)

	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for idx := range ch {
				if pf, ok := parseFileForIndex(files[idx], maxBytes); ok {
					results[idx] = pf
					valid[idx] = true
				}
			}
		}()
	}
	wg.Wait()

	out := make([]parsedFile, 0, len(files))
	for i, ok := range valid {
		if ok {
			out = append(out, results[i])
		}
	}
	return out
}

func assembleIndex(roots []string, parsed []parsedFile, base *Index, buildMs uint64) *Index {
	nf := len(parsed)
	filesB := NewStrsBuilder(nf)
	fileMtimes := make([]int64, 0, nf)
	fileSizes := make([]int64, 0, nf)
	fileIsCode := make([]bool, 0, nf)

	nsEst := 0
	for _, pf := range parsed {
		nsEst += len(pf.chunks)
	}

	secFile := make([]uint32, 0, nsEst)
	secStart := make([]uint32, 0, nsEst)
	secEnd := make([]uint32, 0, nsEst)
	secLen := make([]uint32, 0, nsEst)
	secHeadB := NewStrsBuilder(nsEst)

	termID := make(map[string]uint32, nsEst*8)
	termsB := NewStrsBuilder(nsEst * 8)

	entityID := make(map[string]uint32, nsEst*2)
	entitiesB := NewStrsBuilder(nsEst * 2)

	type secPosting struct {
		sec uint32
		tf  uint32
	}
	var termPostings [][]secPosting
	secHeadTermLists := make([][]uint32, 0, nsEst)
	secEntityLists := make([][]uint32, 0, nsEst)
	var entitySecLists [][]uint32
	var defPairs []kvPair
	defSeen := make(map[string]struct{})

	internTerm := func(t string) uint32 {
		if id, ok := termID[t]; ok {
			return id
		}
		id := termsB.Add(t)
		termID[t] = id
		termPostings = append(termPostings, nil)
		return id
	}

	internEntity := func(e string) uint32 {
		if id, ok := entityID[e]; ok {
			return id
		}
		id := entitiesB.Add(e)
		entityID[e] = id
		entitySecLists = append(entitySecLists, nil)
		return id
	}

	var totalLen uint64
	tfMap := make(map[uint32]uint32, 64)
	headSeen := make(map[uint32]struct{}, 16)

	for _, pf := range parsed {
		fid := filesB.Add(pf.meta.Rel)
		fileMtimes = append(fileMtimes, pf.meta.MtimeNanos)
		fileSizes = append(fileSizes, pf.meta.Size)
		fileIsCode = append(fileIsCode, pf.isCode)

		for _, c := range pf.chunks {
			sid := uint32(len(secFile))
			secFile = append(secFile, fid)
			secStart = append(secStart, c.lineStart)
			secEnd = append(secEnd, c.lineEnd)
			secHeadB.Add(c.heading)

			clear(tfMap)
			clear(headSeen)

			bodyCount := uint32(len(c.bodyTerms))
			if bodyCount == 0 {
				bodyCount = 1
			}
			secLen = append(secLen, bodyCount)
			totalLen += uint64(bodyCount)

			for _, bt := range c.bodyTerms {
				tid := internTerm(bt)
				tfMap[tid]++
			}
			var hIDs []uint32
			for _, ht := range c.headTerms {
				tid := internTerm(ht)
				tfMap[tid] += headingBoost
				if _, ok := headSeen[tid]; !ok {
					headSeen[tid] = struct{}{}
					hIDs = append(hIDs, tid)
				}
			}
			secHeadTermLists = append(secHeadTermLists, hIDs)

			for tid, tf := range tfMap {
				termPostings[tid] = append(termPostings[tid], secPosting{sec: sid, tf: tf})
			}

			eIDs := make([]uint32, 0, len(c.entities))
			for _, ename := range c.entities {
				eid := internEntity(ename)
				eIDs = append(eIDs, eid)
				entitySecLists[eid] = append(entitySecLists[eid], sid)
			}
			secEntityLists = append(secEntityLists, eIDs)

			if c.defName != "" {
				if _, ok := defSeen[c.defName]; !ok {
					defSeen[c.defName] = struct{}{}
					defPairs = append(defPairs, kvPair{key: c.defName, val: sid})
				}
			}
		}
	}

	ns := len(secFile)
	nt := termsB.Len()
	ne := entitiesB.Len()

	avgLen := 1.0
	if base != nil && base.AvgLen > 0 {
		avgLen = base.AvgLen
	} else if ns > 0 {
		avgLen = float64(totalLen) / float64(ns)
		if avgLen < 1.0 {
			avgLen = 1.0
		}
	}

	idfN := float64(max(ns, 1))
	if base != nil && base.NumSections() > 0 {
		idfN = float64(base.NumSections())
	}

	lenNorm := make([]float64, ns)
	for s := range ns {
		b := bm25B
		if fileIsCode[secFile[s]] {
			b = bm25BCode
		}
		lenNorm[s] = bm25K1 * (1.0 - b + b*float64(secLen[s])/avgLen)
	}

	termNames := termsB.Build()
	termMap := buildStrMapDirect(&termNames)
	termDF := make([]uint32, nt)
	postOffsets := make([]uint32, nt+1)
	totalPosts := 0
	for t := range nt {
		totalPosts += len(termPostings[t])
	}
	postSecs := make([]uint32, 0, totalPosts)
	postWeights := make([]float32, 0, totalPosts)

	for t := range nt {
		posts := termPostings[t]
		df := float64(len(posts))
		if base != nil {
			if btid, ok := base.TermMap.Lookup(termNames.Get(uint32(t))); ok && int(btid) < len(base.TermDF) {
				if float64(base.TermDF[btid]) > df {
					df = float64(base.TermDF[btid])
				}
			}
		}
		termDF[t] = uint32(len(posts))
		idf := math.Log((idfN-df+0.5)/(df+0.5) + 1.0)
		if idf < 0 {
			idf = 0
		}
		postOffsets[t] = uint32(len(postSecs))
		for _, p := range posts {
			tf := float64(p.tf)
			w := idf * tf * (bm25K1 + 1.0) / (tf + lenNorm[p.sec])
			postSecs = append(postSecs, p.sec)
			postWeights = append(postWeights, float32(w))
		}
	}
	postOffsets[nt] = uint32(len(postSecs))

	// Entity map + aliases.
	entityNames := entitiesB.Build()
	var entPairs []kvPair
	entPairSeen := make(map[string]struct{}, ne*2)
	for eid := range uint32(ne) {
		ename := entityNames.Get(eid)
		if _, ok := entPairSeen[ename]; !ok {
			entPairSeen[ename] = struct{}{}
			entPairs = append(entPairs, kvPair{key: ename, val: eid})
		}
		if alias := EntityAlias(ename); alias != "" {
			if _, ok := entPairSeen[alias]; !ok {
				entPairSeen[alias] = struct{}{}
				entPairs = append(entPairs, kvPair{key: alias, val: eid})
			}
		}
	}
	entityMap := buildStrMapKV(entPairs)

	// Hub pruning and bipartite + sequential graph.
	hubLimit := max(ns/40, 30)
	entityIsHub := make([]bool, ne)
	for e := range ne {
		if len(entitySecLists[e]) > hubLimit {
			entityIsHub[e] = true
		}
	}

	graphOffsets := make([]uint32, ns+ne+1)
	var graphVals []uint32
	for s := range ns {
		graphOffsets[s] = uint32(len(graphVals))
		if s > 0 && secFile[s-1] == secFile[s] {
			graphVals = append(graphVals, uint32(s-1))
		}
		if s+1 < ns && secFile[s+1] == secFile[s] {
			graphVals = append(graphVals, uint32(s+1))
		}
		for _, e := range secEntityLists[s] {
			if !entityIsHub[e] {
				graphVals = append(graphVals, uint32(ns)+e)
			}
		}
	}
	for e := range ne {
		graphOffsets[ns+e] = uint32(len(graphVals))
		if !entityIsHub[e] {
			graphVals = append(graphVals, entitySecLists[e]...)
		}
	}
	graphOffsets[ns+ne] = uint32(len(graphVals))

	return &Index{
		Roots:        append([]string(nil), roots...),
		BuildMs:      buildMs,
		AvgLen:       avgLen,
		TotalLen:     uint32(totalLen),
		Files:        filesB.Build(),
		FileMtimes:   fileMtimes,
		FileSizes:    fileSizes,
		FileIsCode:   fileIsCode,
		SecFile:      secFile,
		SecStart:     secStart,
		SecEnd:       secEnd,
		SecLen:       secLen,
		SecHeadings:  secHeadB.Build(),
		SecHeadTerms: flattenLists(secHeadTermLists),
		SecEntities:  flattenLists(secEntityLists),
		EntityNames:  entityNames,
		EntityMap:    entityMap,
		EntitySecs:   flattenLists(entitySecLists),
		EntityIsHub:  entityIsHub,
		TermNames:    termNames,
		TermMap:      termMap,
		TermDF:       termDF,
		PostOffsets:  postOffsets,
		PostSecs:     postSecs,
		PostWeights:  postWeights,
		Graph:        Lists{Offsets: graphOffsets, Values: graphVals},
		DefMap:       buildStrMapKV(defPairs),
	}
}

func flattenLists(lists [][]uint32) Lists {
	offsets := make([]uint32, len(lists)+1)
	total := 0
	for _, l := range lists {
		total += len(l)
	}
	vals := make([]uint32, 0, total)
	for i, l := range lists {
		offsets[i] = uint32(len(vals))
		vals = append(vals, l...)
	}
	offsets[len(lists)] = uint32(len(vals))
	return Lists{Offsets: offsets, Values: vals}
}

// Diff compares scanned files against the index signature.
func (idx *Index) Diff(files []FileMeta) (changed []FileMeta, deletedRels []string) {
	nf := idx.NumFiles()
	existing := make(map[string]int, nf)
	for i := range nf {
		existing[idx.Files.Get(uint32(i))] = i
	}
	seen := make(map[string]struct{}, len(files))
	for _, f := range files {
		seen[f.Rel] = struct{}{}
		if fi, ok := existing[f.Rel]; !ok {
			changed = append(changed, f)
		} else if idx.FileMtimes[fi] != f.MtimeNanos || idx.FileSizes[fi] != f.Size {
			changed = append(changed, f)
		}
	}
	for rel := range existing {
		if _, ok := seen[rel]; !ok {
			deletedRels = append(deletedRels, rel)
		}
	}
	return changed, deletedRels
}

// BuildOverlay builds an incremental Overlay on top of base.
func BuildOverlay(base *Index, changed []FileMeta, deletedRels []string, maxBytes int64) *Overlay {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxFileBytes
	}
	changedSet := make(map[string]struct{}, len(changed))
	for _, f := range changed {
		changedSet[f.Rel] = struct{}{}
	}
	deletedSet := make(map[string]struct{}, len(deletedRels))
	for _, r := range deletedRels {
		deletedSet[r] = struct{}{}
	}

	excluded := make([]bool, base.NumSections())
	for s := range base.NumSections() {
		rel := base.Files.Get(base.SecFile[s])
		if _, ok := changedSet[rel]; ok {
			excluded[s] = true
		} else if _, ok := deletedSet[rel]; ok {
			excluded[s] = true
		}
	}

	parsed := parseFilesParallel(changed, maxBytes)
	ovIdx := assembleIndex(base.Roots, parsed, base, 0)
	return &Overlay{
		Base:        base,
		Index:       ovIdx,
		Excluded:    excluded,
		ChangedRels: changedSet,
		DeletedRels: deletedSet,
		CreatedAt:   time.Now(),
		FileCount:   len(changed) + len(deletedRels),
	}
}

type scoredSec struct {
	sec   uint32
	score float64
}

type searchScratch struct {
	bm25    []float64
	matched []uint32
	touched []uint32

	pprR     []float64
	pprP     []float64
	pprInQ   []bool
	pprQ     []uint32
	pprTouch []uint32

	cooc       []uint32
	coocTouch  []uint32
	final      []float64
	finalTouch []uint32
}

var scratchPool = sync.Pool{
	New: func() any { return &searchScratch{} },
}

func getScratch(ns, nNodes int) *searchScratch {
	sc := scratchPool.Get().(*searchScratch)
	if cap(sc.bm25) < ns {
		sc.bm25 = make([]float64, ns)
		sc.matched = make([]uint32, ns)
		sc.cooc = make([]uint32, ns)
		sc.final = make([]float64, ns)
	} else {
		sc.bm25 = sc.bm25[:ns]
		sc.matched = sc.matched[:ns]
		sc.cooc = sc.cooc[:ns]
		sc.final = sc.final[:ns]
	}
	if cap(sc.pprR) < nNodes {
		sc.pprR = make([]float64, nNodes)
		sc.pprP = make([]float64, nNodes)
		sc.pprInQ = make([]bool, nNodes)
	} else {
		sc.pprR = sc.pprR[:nNodes]
		sc.pprP = sc.pprP[:nNodes]
		sc.pprInQ = sc.pprInQ[:nNodes]
	}
	sc.touched = sc.touched[:0]
	sc.pprQ = sc.pprQ[:0]
	sc.pprTouch = sc.pprTouch[:0]
	sc.coocTouch = sc.coocTouch[:0]
	sc.finalTouch = sc.finalTouch[:0]
	return sc
}

func putScratch(sc *searchScratch) {
	for _, s := range sc.touched {
		sc.bm25[s] = 0
		sc.matched[s] = 0
	}
	for _, u := range sc.pprTouch {
		sc.pprR[u] = 0
		sc.pprP[u] = 0
		sc.pprInQ[u] = false
	}
	for _, s := range sc.coocTouch {
		sc.cooc[s] = 0
	}
	for _, s := range sc.finalTouch {
		sc.final[s] = 0
	}
	scratchPool.Put(sc)
}

// Search executes a query against idx alone.
func (idx *Index) Search(query string, opts SearchOpts) Results {
	return SearchCombined(idx, nil, query, opts)
}

// SearchCombined executes a query against base + optional overlay.
func SearchCombined(base *Index, ov *Overlay, query string, opts SearchOpts) Results {
	t0 := time.Now()
	if base == nil {
		return Results{}
	}
	k := opts.K
	if k <= 0 {
		k = 6
	}
	maxPerFile := opts.MaxPerFile
	if maxPerFile <= 0 {
		maxPerFile = defaultMaxPerDoc
	}
	useGraph := opts.useGraph()

	expanded := WithTranslations(query)
	qtermsRaw := Terms(expanded)
	qtermSeen := make(map[string]struct{}, len(qtermsRaw))
	qterms := make([]string, 0, len(qtermsRaw))
	for _, qt := range qtermsRaw {
		if _, ok := qtermSeen[qt]; !ok {
			qtermSeen[qt] = struct{}{}
			qterms = append(qterms, qt)
		}
	}

	qEntities := queryEntities(base, query)
	entNames := make([]string, len(qEntities))
	for i, eid := range qEntities {
		entNames[i] = base.EntityNames.Get(eid)
	}

	baseRes := searchSingle(base, ov, false, query, qterms, qEntities, opts, k, maxPerFile, useGraph)
	if ov == nil || ov.Index == nil || ov.Index.NumSections() == 0 {
		baseRes.Entities = entNames
		baseRes.Micros = time.Since(t0).Microseconds()
		baseRes.QTerms = len(qterms)
		return baseRes
	}

	ovEntities := queryEntities(ov.Index, query)
	ovRes := searchSingle(ov.Index, nil, true, query, qterms, ovEntities, opts, k, maxPerFile, useGraph)

	merged := make([]Hit, 0, len(baseRes.Hits)+len(ovRes.Hits))
	merged = append(merged, baseRes.Hits...)
	merged = append(merged, ovRes.Hits...)
	sort.Slice(merged, func(i, j int) bool {
		if merged[i].Score != merged[j].Score {
			return merged[i].Score > merged[j].Score
		}
		if merged[i].Path != merged[j].Path {
			return merged[i].Path < merged[j].Path
		}
		return merged[i].LineStart < merged[j].LineStart
	})
	if len(merged) > k {
		merged = merged[:k]
	}

	return Results{
		Hits:     merged,
		Entities: entNames,
		Micros:   time.Since(t0).Microseconds(),
		QTerms:   len(qterms),
	}
}

func queryEntities(idx *Index, query string) []uint32 {
	words := RawWords(strings.ToLower(query))
	if len(words) == 0 {
		return nil
	}
	seen := make(map[uint32]struct{})
	var out []uint32
	addEnt := func(eid uint32) {
		if _, ok := seen[eid]; !ok {
			seen[eid] = struct{}{}
			out = append(out, eid)
		}
	}

	i := 0
	for i < len(words) {
		matched := false
		for span := min(5, len(words)-i); span >= 1; span-- {
			cand := strings.Join(words[i:i+span], " ")
			if eid, ok := idx.EntityMap.Lookup(cand); ok {
				addEnt(eid)
				i += span
				matched = true
				break
			}
		}
		if !matched {
			for _, part := range splitJoiners(words[i]) {
				if eid, ok := idx.EntityMap.Lookup(part); ok {
					addEnt(eid)
				}
			}
			i++
		}
	}
	return out
}

func searchSingle(
	idx *Index,
	ov *Overlay,
	isOverlay bool,
	rawQuery string,
	qterms []string,
	qEntities []uint32,
	opts SearchOpts,
	k, maxPerFile int,
	useGraph bool,
) Results {
	ns := idx.NumSections()
	ne := idx.NumEntities()
	if ns == 0 {
		return Results{}
	}
	nNodes := ns + ne
	sc := getScratch(ns, nNodes)
	defer putScratch(sc)

	// Query term IDs & IDFs.
	type qtInfo struct {
		tid uint32
		idf float64
	}
	var qts []qtInfo
	idfTotal := 0.0
	idfN := float64(max(ns, 1))
	for _, qt := range qterms {
		if tid, ok := idx.TermMap.Lookup(qt); ok {
			df := float64(idx.TermDF[tid])
			idf := math.Log((idfN-df+0.5)/(df+0.5) + 1.0)
			if idf < 0 {
				idf = 0
			}
			qts = append(qts, qtInfo{tid: tid, idf: idf})
			idfTotal += idf
		}
	}
	if idfTotal < 1e-9 {
		idfTotal = 1.0
	}
	nQuery := float64(max(len(qterms), 1))

	// 1. Lexical BM25 accumulation.
	for _, qt := range qts {
		start := idx.PostOffsets[qt.tid]
		end := idx.PostOffsets[qt.tid+1]
		for p := start; p < end; p++ {
			s := idx.PostSecs[p]
			if !isOverlay && ov != nil && int(s) < len(ov.Excluded) && ov.Excluded[s] {
				continue
			}
			if sc.matched[s] == 0 {
				sc.touched = append(sc.touched, s)
			}
			sc.bm25[s] += float64(idx.PostWeights[p])
			sc.matched[s]++
		}
	}

	lexHits := make([]scoredSec, 0, min(len(sc.touched), topLexicalCap))
	for _, s := range sc.touched {
		coord := 0.5 + 0.5*(float64(sc.matched[s])/nQuery)
		score := sc.bm25[s] * coord
		if score > 0 {
			lexHits = append(lexHits, scoredSec{sec: s, score: score})
		}
	}
	sort.Slice(lexHits, func(i, j int) bool {
		if lexHits[i].score != lexHits[j].score {
			return lexHits[i].score > lexHits[j].score
		}
		return lexHits[i].sec < lexHits[j].sec
	})
	if len(lexHits) > topLexicalCap {
		lexHits = lexHits[:topLexicalCap]
	}

	topLex := 1e-6
	if len(lexHits) > 0 && lexHits[0].score > topLex {
		topLex = lexHits[0].score
	}

	addFinal := func(s uint32, delta float64) {
		if sc.final[s] == 0 {
			sc.finalTouch = append(sc.finalTouch, s)
		}
		sc.final[s] += delta
	}

	for _, lh := range lexHits {
		addFinal(lh.sec, lh.score/topLex)
	}

	// 2. Personalized PageRank (Andersen-Chung-Lang forward push).
	if useGraph && (len(qEntities) > 0 || len(lexHits) > 0) {
		entityMass := 0.0
		pushSeed := func(u uint32, mass float64) {
			if mass <= 0 {
				return
			}
			if sc.pprR[u] == 0 && sc.pprP[u] == 0 {
				sc.pprTouch = append(sc.pprTouch, u)
			}
			sc.pprR[u] += mass
			if !sc.pprInQ[u] {
				sc.pprInQ[u] = true
				sc.pprQ = append(sc.pprQ, u)
			}
		}

		for _, eid := range qEntities {
			secs := idx.EntitySecs.Get(eid)
			if len(secs) > 0 && !idx.EntityIsHub[eid] {
				w := math.Max(math.Log(float64(ns)/float64(len(secs))), 0.1)
				pushSeed(uint32(ns)+eid, w)
				entityMass += w
			}
		}

		lexSeedCount := min(len(lexHits), 5)
		if lexSeedCount > 0 {
			lexWeight := 1.0
			if entityMass > 0 {
				lexWeight = 0.3 * entityMass
			}
			sumLex := 0.0
			for i := range lexSeedCount {
				sumLex += lexHits[i].score
			}
			if sumLex > 0 {
				for i := range lexSeedCount {
					pushSeed(lexHits[i].sec, lexWeight*lexHits[i].score/sumLex)
				}
			}
		}

		// Forward push loop.
		head := 0
		pushes := 0
		for head < len(sc.pprQ) && pushes < pprMaxPushes {
			u := sc.pprQ[head]
			head++
			sc.pprInQ[u] = false
			ru := sc.pprR[u]
			nbrs := idx.Graph.Get(u)
			deg := len(nbrs)
			if deg == 0 {
				sc.pprP[u] += ru
				sc.pprR[u] = 0
				continue
			}
			if ru/float64(deg) < pprEps {
				continue
			}
			sc.pprR[u] = 0
			sc.pprP[u] += pprAlpha * ru
			dist := (1.0 - pprAlpha) * ru / float64(deg)
			for _, v := range nbrs {
				if sc.pprR[v] == 0 && sc.pprP[v] == 0 {
					sc.pprTouch = append(sc.pprTouch, v)
				}
				sc.pprR[v] += dist
				vnbrs := idx.Graph.Get(v)
				if len(vnbrs) > 0 && sc.pprR[v]/float64(len(vnbrs)) >= pprEps && !sc.pprInQ[v] {
					sc.pprInQ[v] = true
					sc.pprQ = append(sc.pprQ, v)
				}
			}
			pushes++
		}

		topPPR := 0.0
		for _, u := range sc.pprTouch {
			if int(u) < ns {
				if !isOverlay && ov != nil && int(u) < len(ov.Excluded) && ov.Excluded[u] {
					continue
				}
				if sc.pprP[u] > topPPR {
					topPPR = sc.pprP[u]
				}
			}
		}
		if topPPR > 0 {
			for _, u := range sc.pprTouch {
				if int(u) < ns && sc.pprP[u] > 0 {
					if !isOverlay && ov != nil && int(u) < len(ov.Excluded) && ov.Excluded[u] {
						continue
					}
					addFinal(u, 0.4*sc.pprP[u]/topPPR)
				}
			}
		}
	}

	// 3. E2GraphRAG co-occurrence.
	if useGraph && len(qEntities) >= 2 {
		for _, eid := range qEntities {
			for _, s := range idx.EntitySecs.Get(eid) {
				if !isOverlay && ov != nil && int(s) < len(ov.Excluded) && ov.Excluded[s] {
					continue
				}
				if sc.cooc[s] == 0 {
					sc.coocTouch = append(sc.coocTouch, s)
				}
				sc.cooc[s]++
			}
		}
		for _, s := range sc.coocTouch {
			if sc.cooc[s] >= 2 {
				addFinal(s, 0.25*float64(sc.cooc[s]-1))
			}
		}
	}

	// 4. Go-to-definition boost (+1.0).
	qWords := RawWords(rawQuery)
	singleWord := len(qWords) == 1
	for _, w := range qWords {
		if singleWord || looksLikeName(w) {
			last := w
			if idxColon := strings.LastIndex(w, "::"); idxColon >= 0 {
				last = w[idxColon+2:]
			}
			last = strings.ToLower(last)
			if sid, ok := idx.DefMap.Lookup(last); ok {
				if !isOverlay && ov != nil && int(sid) < len(ov.Excluded) && ov.Excluded[sid] {
					continue
				}
				addFinal(sid, 1.0)
			}
		}
	}

	// 5. Heading term boost (+0.3).
	if len(qts) > 0 {
		qidfMap := make(map[uint32]float64, len(qts))
		for _, qt := range qts {
			qidfMap[qt.tid] = qt.idf
		}
		for _, s := range sc.finalTouch {
			hsum := 0.0
			for _, htid := range idx.SecHeadTerms.Get(s) {
				if idf, ok := qidfMap[htid]; ok {
					hsum += idf
				}
			}
			if hsum > 0 {
				sc.final[s] += 0.3 * hsum / idfTotal
			}
		}
	}

	// Collect, filter, and sort.
	candidates := make([]scoredSec, 0, len(sc.finalTouch))
	prefix := strings.TrimPrefix(filepath.ToSlash(opts.PathPrefix), "./")
	prefix = strings.TrimPrefix(prefix, "/")
	for _, s := range sc.finalTouch {
		if sc.final[s] <= 0 {
			continue
		}
		if prefix != "" {
			rel := idx.Files.Get(idx.SecFile[s])
			if !strings.HasPrefix(rel, prefix) {
				continue
			}
		}
		if opts.AbsPathPrefix != "" && !pathStartsWith(resolveAbsFromRoots(idx.Roots, idx.Files.Get(idx.SecFile[s])), opts.AbsPathPrefix) {
			continue
		}
		candidates = append(candidates, scoredSec{sec: s, score: sc.final[s]})
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].sec < candidates[j].sec
	})

	qEntSet := make(map[uint32]struct{}, len(qEntities))
	for _, eid := range qEntities {
		qEntSet[eid] = struct{}{}
	}
	qTermSet := make(map[uint32]float64, len(qts))
	for _, qt := range qts {
		qTermSet[qt.tid] = qt.idf
	}

	perFile := make(map[uint32]int)
	hits := make([]Hit, 0, min(k, len(candidates)))
	for _, cand := range candidates {
		fid := idx.SecFile[cand.sec]
		if perFile[fid] >= maxPerFile {
			continue
		}
		perFile[fid]++

		rel := idx.Files.Get(fid)
		abs := resolveAbsFromRoots(idx.Roots, rel)
		lStart := idx.SecStart[cand.sec]
		lEnd := idx.SecEnd[cand.sec]
		heading := idx.SecHeadings.Get(cand.sec)

		named := 0
		for _, eid := range idx.SecEntities.Get(cand.sec) {
			if !idx.EntityIsHub[eid] {
				if _, ok := qEntSet[eid]; ok {
					named++
				}
			}
		}

		// Compute term coverage & read snippet text.
		text := readSectionLines(abs, lStart, lEnd)
		cov := computeCoverage(text, heading, qTermSet, idx, idfTotal)

		text, truncated := cutSectionText(text, opts.MaxChars)

		hits = append(hits, Hit{
			Path:      rel,
			AbsPath:   abs,
			LineStart: lStart,
			LineEnd:   lEnd,
			Heading:   heading,
			Score:     cand.score,
			Coverage:  cov,
			Named:     named,
			Text:      text,
			Truncated: truncated,
		})
		if len(hits) >= k {
			break
		}
	}

	return Results{Hits: hits}
}

func looksLikeName(w string) bool {
	if strings.ContainsAny(w, "_:") {
		return true
	}
	if hasASCIIDigit(w) {
		return true
	}
	return len(CamelParts(w)) > 1
}

// cutSectionText is the docsearch addon's cut (node/src/lib.rs): at most maxChars bytes (default 1500, at least 200),
// on a character boundary, and at a line break when one is in the second half.
func cutSectionText(text string, maxChars int) (string, bool) {
	if maxChars <= 0 {
		maxChars = 1500
	}
	maxChars = max(maxChars, 200)
	if len(text) <= maxChars {
		return text, false
	}
	end := maxChars
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	if nl := strings.LastIndexByte(text[:end], '\n'); nl > maxChars/2 {
		end = nl
	}
	return text[:end], true
}

// pathStartsWith is Rust's Path::starts_with: prefix is path or one of its parent folders.
func pathStartsWith(path, prefix string) bool {
	path, prefix = filepath.Clean(path), filepath.Clean(prefix)
	if path == prefix {
		return true
	}
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	return strings.HasPrefix(path, prefix)
}

func resolveAbsFromRoots(roots []string, rel string) string {
	if len(roots) == 0 {
		return rel
	}
	if len(roots) == 1 {
		return filepath.Join(roots[0], filepath.FromSlash(rel))
	}
	slashIdx := strings.IndexByte(rel, '/')
	if slashIdx > 0 {
		head := rel[:slashIdx]
		rest := rel[slashIdx+1:]
		for _, r := range roots {
			if filepath.Base(r) == head {
				return filepath.Join(r, filepath.FromSlash(rest))
			}
		}
	}
	return filepath.Join(roots[0], filepath.FromSlash(rel))
}

func readSectionLines(absPath string, lineStart, lineEnd uint32) string {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if lineStart == 0 {
		lineStart = 1
	}
	s := int(lineStart) - 1
	e := int(lineEnd)
	if s >= len(lines) {
		return ""
	}
	if e > len(lines) {
		e = len(lines)
	}
	if e <= s {
		return ""
	}
	return strings.Join(lines[s:e], "\n")
}

func computeCoverage(text, heading string, qTermIDFs map[uint32]float64, idx *Index, idfTotal float64) float64 {
	if len(qTermIDFs) == 0 || idfTotal <= 0 {
		return 0
	}
	seen := make(map[uint32]struct{}, len(qTermIDFs))
	sum := 0.0
	for _, t := range Terms(heading + "\n" + text) {
		if tid, ok := idx.TermMap.Lookup(t); ok {
			if idf, inQ := qTermIDFs[tid]; inQ {
				if _, dup := seen[tid]; !dup {
					seen[tid] = struct{}{}
					sum += idf
				}
			}
		}
	}
	cov := sum / idfTotal
	if cov > 1.0 {
		cov = 1.0
	}
	return cov
}
