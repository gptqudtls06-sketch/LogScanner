package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"logscanner/internal/analyzer"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type focusArea int

const (
	focusTable focusArea = iota
	focusTail
)

type tailItem struct {
	Seq  uint64
	Text string
}

var (
	cTitle = lipgloss.NewStyle().Bold(true)
	cDim   = lipgloss.NewStyle().Faint(true)

	box = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1)

	headerBar = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), false, false, true, false).
			Padding(0, 1)

	badgeOK    = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true)
	badgeRun   = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)
	badgePause = lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Bold(true)

	badgeWarn = lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Bold(true)
	badgeErr  = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)

	keyHint = lipgloss.NewStyle().Faint(true)
)

type model struct {
	width  int
	height int

	started time.Time

	prog progress.Model
	spin spinner.Model
	tab  table.Model

	cfg Config

	updates <-chan analyzer.Event
	pauseFn func(bool)

	filesTotal int
	filesDone  int
	linesTotal int64
	matches    int64

	done   bool
	paused bool
	err    error

	rowIndexByFile map[string]int

	tailItems []tailItem
	focus     focusArea

	// tail panel sizing
	tailPanelHeight int
}

func initialModel(files []string, updates <-chan analyzer.Event, cfg Config, pauseFn func(bool)) model {
	p := progress.New(progress.WithDefaultGradient())
	p.Width = 40

	s := spinner.New()
	s.Spinner = spinner.Dot

	sorted := append([]string(nil), files...)
	sort.Strings(sorted)

	cols := []table.Column{
		{Title: "File", Width: 44},
		{Title: "Lines", Width: 10},
		{Title: "Matches", Width: 10},
		{Title: "Status", Width: 8},
	}

	rows := make([]table.Row, 0, len(sorted))
	rowIndex := make(map[string]int, len(sorted))

	for i, f := range sorted {
		rows = append(rows, table.Row{f, "-", "-", "WAIT"})
		rowIndex[f] = i
	}

	t := table.New(table.WithColumns(cols), table.WithRows(rows), table.WithFocused(true))
	t.SetHeight(minInt(12, len(rows)+1))

	st := table.DefaultStyles()
	st.Header = st.Header.Bold(true)
	st.Selected = st.Selected.Bold(true)
	t.SetStyles(st)

	return model{
		started:         time.Now(),
		prog:            p,
		spin:            s,
		tab:             t,
		cfg:             cfg,
		updates:         updates,
		pauseFn:         pauseFn,
		filesTotal:      len(files),
		rowIndexByFile:  rowIndex,
		tailItems:       make([]tailItem, 0, cfg.TailMax),
		focus:           focusTable,
		tailPanelHeight: 10,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.spin.Tick, waitEvent(m.updates))
}

func waitEvent(ch <-chan analyzer.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return analyzer.Totals{Done: true}
		}
		return ev
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.prog.Width = clamp(m.width-12, 20, 90)

		leftW := clamp(m.width/2, 40, 90)
		fileColWidth := clamp(leftW-26, 20, 80)
		cols := m.tab.Columns()
		cols[0].Width = fileColWidth
		m.tab.SetColumns(cols)

		// tail panel height: 화면 높이에 따라 자동
		// 위쪽(header+bar+stats+간격) 대략 6~7줄 + hint 1줄 빼고 남는 공간에서
		// 테이블 높이 고려해 적당히 8~14로 clamp
		m.tailPanelHeight = clamp(m.height-14, 8, 14)

		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "tab":
			if m.focus == focusTable {
				m.focus = focusTail
			} else {
				m.focus = focusTable
			}
			return m, nil
		case "p":
			m.paused = !m.paused
			if m.pauseFn != nil {
				m.pauseFn(m.paused)
			}
			return m, nil
		default:
			// table만 키 처리(이 버전은 tail 스크롤은 고정 패널이라 생략)
			if m.focus == focusTable {
				var cmd tea.Cmd
				m.tab, cmd = m.tab.Update(msg)
				return m, cmd
			}
			return m, nil
		}

	case analyzer.FileUpdate:
		u := msg
		if idx, ok := m.rowIndexByFile[u.File]; ok {
			rows := m.tab.Rows()
			lines := "-"
			matches := "-"
			status := u.Status
			if status == "" {
				status = "WAIT"
			}
			if status != "WAIT" {
				lines = fmt.Sprintf("%d", u.Lines)
				matches = fmt.Sprintf("%d", u.Matches)
			}
			rows[idx] = table.Row{u.File, lines, matches, status}
			m.tab.SetRows(rows)
		}
		return m, tea.Batch(waitEvent(m.updates))

	case analyzer.MatchLine:
		text := fmt.Sprintf("[%s] %s", msg.File, msg.Line)
		m.tailItems = append(m.tailItems, tailItem{Seq: msg.Seq, Text: text})

		// Seq 정렬 (병렬 순서 보정)
		sort.Slice(m.tailItems, func(i, j int) bool { return m.tailItems[i].Seq < m.tailItems[j].Seq })
		if len(m.tailItems) > m.cfg.TailMax {
			m.tailItems = m.tailItems[len(m.tailItems)-m.cfg.TailMax:]
		}

		return m, tea.Batch(waitEvent(m.updates))

	case analyzer.Totals:
		s := msg
		if s.Err != nil {
			m.err = s.Err
		}

		m.filesDone = s.FilesDone
		m.linesTotal = s.LinesTotal
		m.matches = s.MatchesTotal

		var percent float64
		if m.filesTotal > 0 {
			percent = float64(m.filesDone) / float64(m.filesTotal)
		}
		cmd := m.prog.SetPercent(percent)

		if s.Done {
			m.done = true
			return m, cmd // 자동 종료 X
		}
		return m, tea.Batch(cmd, waitEvent(m.updates))

	default:
		return m, nil
	}
}

func (m model) View() string {
	// 상태 배지
	statusBadge := badgeRun.Render(" SCANNING ")
	if m.paused {
		statusBadge = badgePause.Render(" PAUSED ")
	}
	if m.done {
		statusBadge = badgeOK.Render(" DONE ")
	}

	headLeft := cTitle.Render("Go-LogScanner") + " " + statusBadge
	headRight := cDim.Render(fmt.Sprintf("keyword=%s  workers=%d  tail=%d", m.cfg.Keyword, m.cfg.Concurrent, m.cfg.TailMax))
	header := headerBar.Width(maxInt(0, m.width-2)).Render(headLeft + "\n" + headRight)

	elapsed := time.Since(m.started).Truncate(100 * time.Millisecond)
	percent := 0.0
	if m.filesTotal > 0 {
		percent = float64(m.filesDone) / float64(m.filesTotal)
	}
	bar := m.prog.ViewAs(percent)

	stats := fmt.Sprintf("Files %d/%d  Lines %d  Matches %d  Elapsed %s",
		m.filesDone, m.filesTotal, m.linesTotal, m.matches, elapsed)

	top := joinLines(header, "", bar, cDim.Render(stats))

	// layout widths
	leftW := clamp(m.width/2, 40, 90)
	rightW := maxInt(30, m.width-leftW-3)

	// table box
	tableBox := box.Width(leftW).Render(cTitle.Render("Files") + "\n" + m.tab.View())

	// tail panel content (고정 높이)
	tailLines := m.renderTailLines(m.tailPanelHeight)
	tailContent := strings.Join(tailLines, "\n")
	tailBox := box.Width(rightW).Render(cTitle.Render("Recent Matches") + "\n" + tailContent)

	row := lipgloss.JoinHorizontal(lipgloss.Top, tableBox, " ", tailBox)

	hint := ""
	if m.done {
		hint = keyHint.Render("Done. Press q to quit | tab focus | ↑/↓ table | p pause/resume")
	} else {
		hint = keyHint.Render("Keys: q quit | tab focus | ↑/↓ table | p pause/resume")
	}

	if m.err != nil {
		return joinLines(top, "", badgeErr.Render("ERROR: "+m.err.Error()), "", row, "", hint)
	}

	return joinLines(top, "", row, "", hint)
}

func (m model) renderTailLines(height int) []string {
	// tailItems는 이미 Seq 정렬된 상태라고 가정(Update에서 정렬함)
	if height <= 0 {
		return []string{}
	}

	// 패널에 보여줄 만큼만: "최근 height 줄"
	start := 0
	if len(m.tailItems) > height {
		start = len(m.tailItems) - height
	}

	out := make([]string, 0, height)
	for _, it := range m.tailItems[start:] {
		out = append(out, highlight(it.Text))
	}

	// 항상 height 줄 채우기(빈 줄로 채워서 박스가 안정적으로 보이게)
	for len(out) < height {
		out = append(out, "")
	}
	return out
}

func highlight(line string) string {
	if strings.Contains(line, "ERROR") {
		return badgeErr.Render(line)
	}
	if strings.Contains(line, "WARN") {
		return badgeWarn.Render(line)
	}
	return line
}

func joinLines(lines ...string) string { return strings.Join(lines, "\n") }

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
