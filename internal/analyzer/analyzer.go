package analyzer

import (
	"bufio"
	"os"
	"regexp"
	"sync"
	"sync/atomic"
	"time"
)

type PauseController struct {
	paused int32
	chMu   sync.Mutex
	ch     chan struct{} // resume 시 close
}

func NewPauseController() *PauseController {
	return &PauseController{ch: make(chan struct{})}
}

func (p *PauseController) SetPaused(v bool) {
	if v {
		atomic.StoreInt32(&p.paused, 1)
		return
	}
	atomic.StoreInt32(&p.paused, 0)

	p.chMu.Lock()
	select {
	case <-p.ch:
	default:
		close(p.ch)
	}
	p.ch = make(chan struct{})
	p.chMu.Unlock()
}

func (p *PauseController) WaitIfPaused() {
	if atomic.LoadInt32(&p.paused) == 0 {
		return
	}
	p.chMu.Lock()
	ch := p.ch
	p.chMu.Unlock()
	<-ch
}

// Start: 워커풀 + Totals ticker(select로 종료) + MatchLine Seq 보장 + pauseFn 반환
func Start(files []string, re *regexp.Regexp, concurrent int) (<-chan Event, func(bool)) {
	out := make(chan Event, 256)
	pc := NewPauseController()
	pauseFn := func(paused bool) { pc.SetPaused(paused) }

	go func() {
		defer close(out)

		var filesDone int64
		var linesTotal int64
		var matchesTotal int64
		var seq uint64

		// 초기 totals
		out <- Totals{FilesTotal: len(files)}

		jobs := make(chan string)
		var wg sync.WaitGroup
		wg.Add(concurrent)

		for i := 0; i < concurrent; i++ {
			go func() {
				defer wg.Done()
				for path := range jobs {
					pc.WaitIfPaused()

					lines, matches, err := scanFileOnce(path, re, out, pc, &seq)
					if err != nil {
						out <- FileUpdate{File: path, Lines: lines, Matches: matches, Status: "FAIL", Err: err}
					} else {
						out <- FileUpdate{File: path, Lines: lines, Matches: matches, Status: "DONE", Err: nil}
						atomic.AddInt64(&linesTotal, lines)
						atomic.AddInt64(&matchesTotal, matches)
					}
					atomic.AddInt64(&filesDone, 1)
				}
			}()
		}

		go func() {
			for _, f := range files {
				out <- FileUpdate{File: f, Status: "WAIT"}
				jobs <- f
			}
			close(jobs)
		}()

		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()

		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()

		for {
			select {
			case <-ticker.C:
				out <- Totals{
					FilesTotal:   len(files),
					FilesDone:    int(atomic.LoadInt64(&filesDone)),
					LinesTotal:   atomic.LoadInt64(&linesTotal),
					MatchesTotal: atomic.LoadInt64(&matchesTotal),
				}
			case <-done:
				out <- Totals{
					FilesTotal:   len(files),
					FilesDone:    int(atomic.LoadInt64(&filesDone)),
					LinesTotal:   atomic.LoadInt64(&linesTotal),
					MatchesTotal: atomic.LoadInt64(&matchesTotal),
					Done:         true,
				}
				return
			}
		}
	}()

	return out, pauseFn
}

func scanFileOnce(path string, re *regexp.Regexp, out chan<- Event, pc *PauseController, seq *uint64) (int64, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	const maxCapacity = 1024 * 1024 * 8
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, maxCapacity)

	var lines int64
	var matches int64

	for scanner.Scan() {
		pc.WaitIfPaused()

		lines++
		txt := scanner.Text()
		if re.MatchString(txt) {
			matches++
			id := atomic.AddUint64(seq, 1)
			out <- MatchLine{Seq: id, File: path, Line: txt}
		}
	}

	if err := scanner.Err(); err != nil {
		return lines, matches, err
	}
	return lines, matches, nil
}
