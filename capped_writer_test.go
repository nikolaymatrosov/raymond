package raymond

import (
	"bytes"
	"errors"
	"testing"
)

// shortWriter writes only the first byte of any non-empty payload and
// returns nil — i.e. (n<len(p), nil). Used to verify that cappedWriter
// surfaces short writes unchanged rather than converting them into
// errBudgetOverflow.
type shortWriter struct{ buf bytes.Buffer }

func (w *shortWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	w.buf.WriteByte(p[0])
	return 1, nil
}

// errWriter returns its configured error after writing nothing.
type errWriter struct{ err error }

func (w *errWriter) Write(p []byte) (int, error) { return 0, w.err }

func TestCappedWriter_StateTransitions(t *testing.T) {
	cases := []struct {
		name       string
		limit      int64
		writes     [][]byte
		wantBytes  string
		wantErrIdx int   // index of the write that returns errBudgetOverflow; -1 = none
		wantNs     []int // expected n returned by each Write
	}{
		{
			name:       "exact-fit single write",
			limit:      5,
			writes:     [][]byte{[]byte("hello")},
			wantBytes:  "hello",
			wantErrIdx: -1,
			wantNs:     []int{5},
		},
		{
			name:       "off-by-one over",
			limit:      5,
			writes:     [][]byte{[]byte("helloX")},
			wantBytes:  "hello",
			wantErrIdx: 0,
			wantNs:     []int{5},
		},
		{
			name:       "off-by-one under",
			limit:      5,
			writes:     [][]byte{[]byte("hell")},
			wantBytes:  "hell",
			wantErrIdx: -1,
			wantNs:     []int{4},
		},
		{
			name:       "multi-write accumulating to limit",
			limit:      6,
			writes:     [][]byte{[]byte("foo"), []byte("bar")},
			wantBytes:  "foobar",
			wantErrIdx: -1,
			wantNs:     []int{3, 3},
		},
		{
			name:       "multi-write second straddles limit",
			limit:      4,
			writes:     [][]byte{[]byte("foo"), []byte("bar")},
			wantBytes:  "foob",
			wantErrIdx: 1,
			wantNs:     []int{3, 1},
		},
		{
			name:       "zero budget rejects non-empty write",
			limit:      0,
			writes:     [][]byte{[]byte("x")},
			wantBytes:  "",
			wantErrIdx: 0,
			wantNs:     []int{0},
		},
		{
			name:       "zero budget accepts empty write",
			limit:      0,
			writes:     [][]byte{nil},
			wantBytes:  "",
			wantErrIdx: -1,
			wantNs:     []int{0},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			cw := newCappedWriter(&buf, tc.limit)
			for i, p := range tc.writes {
				n, err := cw.Write(p)
				if n != tc.wantNs[i] {
					t.Errorf("write %d: n = %d, want %d", i, n, tc.wantNs[i])
				}
				if i == tc.wantErrIdx {
					if !errors.Is(err, errBudgetOverflow) {
						t.Errorf("write %d: err = %v, want errBudgetOverflow", i, err)
					}
				} else if err != nil {
					t.Errorf("write %d: unexpected err = %v", i, err)
				}
			}
			if got := buf.String(); got != tc.wantBytes {
				t.Errorf("buffer = %q, want %q", got, tc.wantBytes)
			}
		})
	}
}

func TestCappedWriter_UnderlyingErrorSurfaced(t *testing.T) {
	myErr := errors.New("dst exploded")
	cw := newCappedWriter(&errWriter{err: myErr}, 100)
	n, err := cw.Write([]byte("hello"))
	if n != 0 {
		t.Errorf("n = %d, want 0", n)
	}
	if !errors.Is(err, myErr) {
		t.Errorf("err = %v, want %v", err, myErr)
	}
	if errors.Is(err, errBudgetOverflow) {
		t.Errorf("underlying writer error must NOT be reported as errBudgetOverflow")
	}
}

func TestCappedWriter_UnderlyingShortWriteSurfaced(t *testing.T) {
	sw := &shortWriter{}
	cw := newCappedWriter(sw, 100)
	n, err := cw.Write([]byte("hello"))
	if n != 1 {
		t.Errorf("n = %d, want 1", n)
	}
	if err != nil {
		t.Errorf("expected nil err on under-budget short write, got %v", err)
	}
	if sw.buf.String() != "h" {
		t.Errorf("dst received %q, want %q", sw.buf.String(), "h")
	}
}
