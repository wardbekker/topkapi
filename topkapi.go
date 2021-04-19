package topkapi

import (
	"errors"
	"math"
	"sort"

	"github.com/mitchellh/hashstructure"
)

var incompatibleSketches = errors.New("Incompatible sketches")

type LocalHeavyHitter struct {
	Key   interface{}
	Count uint64
}

type Sketch struct {
	l       uint64 // number of rows
	b       uint64 // think of this as the k
	cms     [][]uint64
	counts  [][]int64
	objects [][]interface{}
}

// New creates a new Topkapi Sketch with given error rate and confidence.
// Accuracy guarantees will be made in terms of a pair of user specified parameters,
// ε and δ, meaning that the error in answering a query is within a factor of ε with
// probability 1-δ
func New(delta, epsilon float64) (*Sketch, error) {
	if epsilon <= 0 || epsilon >= 1 {
		return nil, errors.New("topkapi: value of epsilon should be in range of (0, 1)")
	}
	if delta <= 0 || delta >= 1 {
		return nil, errors.New("topkapi: value of delta should be in range of (0, 1)")
	}

	var (
		b = uint64(math.Ceil(1 / epsilon))
		l = uint64(math.Log(2 / delta))
	)

	//fmt.Printf("b=%d, l=%d, epsilon=%f, delta=%f\n", b, l, epsilon, delta)

	return newSketch(b, l), nil
}

// NewTopK creates a sketch suitable for finding TopK in a corpus of a given size,
// with an error rate of delta.
func NewTopK(k, approxCorpusSize uint64, delta float64) (*Sketch, error) {
	if k < 1 {
		return nil, errors.New("topkapi: value of k should be in >= 1")
	}

	// We want to grow ~ k*log(corpus size)
	// The factor 55 was chosen through experiementation as the minimal threshold where
	// the error rates don't grow out of control on merge and our tests pass.
	// Example: for top-20 on a corpus of 1M we require 15197 buckets and ~475kb space.
	numBuckets := uint64(55.0 * float64(k) * math.Log(float64(approxCorpusSize)))
	numHashFuncs := uint64(4)

	return newSketch(numBuckets, numHashFuncs), nil
}

func newSketch(b, l uint64) *Sketch {
	var (
		cms     = make([][]uint64, l)
		counts  = make([][]int64, l)
		objects = make([][]interface{}, l)
	)

	for i := range counts {
		cms[i] = make([]uint64, b)
		counts[i] = make([]int64, b)
		objects[i] = make([]interface{}, b)
	}

	return &Sketch{
		l:       l,
		b:       b,
		counts:  counts,
		objects: objects,
		cms:     cms,
	}
}

// Epsilon is the approximate error range factor.
func (sk *Sketch) Epsilon() float64 {
	return 1.0 / float64(sk.b)
}

// Delta is the probability for a measurement to be outside the epsilon range
func (sk *Sketch) Delta() float64 {
	return 2.0 / math.Exp(float64(sk.l))
}

// Insert ...
func (sk *Sketch) Insert(key interface{}, count uint64) {
	var (
		hsum, _ = hashstructure.Hash(key, nil)
		h1      = uint32(hsum & 0xffffffff)
		h2      = uint32((hsum >> 32) & 0xffffffff)
	)

	for i := range sk.counts {
		h := uint64((h1 + uint32(i)*h2))
		hi := h % sk.b

		sk.cms[i][hi] += count

		if sk.objects[i][hi] == key {
			sk.counts[i][hi] += int64(count)
		} else {
			sk.counts[i][hi] -= int64(count)
			if sk.counts[i][hi] <= 0 {
				sk.objects[i][hi] = key
				sk.counts[i][hi] = 1
			}
		}
	}
}

// Result ...
func (sk *Sketch) Result(threshold uint64) []LocalHeavyHitter {
	var (
		seen = make(map[interface{}]int)
		cs   = make([]LocalHeavyHitter, 0, sk.b)
	)

	for i := range sk.objects {
		for j, obj := range sk.objects[i] {
			count := sk.cms[i][j]
			if count < threshold {
				continue
			}
			idx, ok := seen[obj]
			if !ok {
				idx = len(cs)
				seen[obj] = idx
				cs = append(cs, LocalHeavyHitter{
					Key:   obj,
					Count: count,
				})
			}
			if count < cs[idx].Count {
				cs[idx].Count = count
			}
		}
	}

	sort.Slice(cs, func(a, b int) bool {
		return cs[a].Count > cs[b].Count
	})

	return cs
}

// Merge ...
func (sk *Sketch) Merge(other *Sketch) error {
	if sk.b != other.b || sk.l != other.l {
		return incompatibleSketches
	}

	// HALP: This is probably wrong - the article doesn't explain how to merge!
	for i := range sk.counts {
		ws := sk.objects[i]
		ows := other.objects[i]
		cnt := sk.counts[i]
		ocnt := other.counts[i]
		cms := sk.cms[i]
		ocms := other.cms[i]
		for j := range cnt {
			if ws[j] == ows[j] {
				cnt[j] += ocnt[j]
				cms[j] += ocms[j]
			} else if cnt[j] < ocnt[j] {
				ws[j] = ows[j]
				cnt[j] = ocnt[j]
				cms[j] = ocms[j]
			}

		}
	}

	return nil
}
