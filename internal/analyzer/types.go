package analyzer

type Event interface{ isEvent() }

type Totals struct {
	FilesTotal   int
	FilesDone    int
	LinesTotal   int64
	MatchesTotal int64
	Done         bool
	Err          error
}

func (Totals) isEvent() {}

type FileUpdate struct {
	File    string
	Lines   int64
	Matches int64
	Status  string // WAIT/DONE/FAIL
	Err     error
}

func (FileUpdate) isEvent() {}

type MatchLine struct {
	Seq  uint64
	File string
	Line string
}

func (MatchLine) isEvent() {}
