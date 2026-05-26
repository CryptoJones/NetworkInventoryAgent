// Package tui implements the interactive terminal UI for the network inventory
// console. It provides the same views as the web admin console: dashboard,
// host inventory, per-host port detail, and full scan history.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Ronin48/NetworkInventoryAgent/internal/store"
	"github.com/Ronin48/NetworkInventoryAgent/models"
)

// --- colours (GitHub dark) ---

var (
	colBg      = lipgloss.Color("#0d1117")
	colSurface = lipgloss.Color("#161b22")
	colBorder  = lipgloss.Color("#30363d")
	colText    = lipgloss.Color("#c9d1d9")
	colSubtle  = lipgloss.Color("#6e7681")
	colGreen   = lipgloss.Color("#3fb950")
	colBlue    = lipgloss.Color("#58a6ff")
	colYellow  = lipgloss.Color("#d29922")
	colRed     = lipgloss.Color("#f85149")
	colPurple  = lipgloss.Color("#bc8cff")
)

// --- styles ---

var (
	styleBase = lipgloss.NewStyle().
			Background(colBg).
			Foreground(colText)

	styleTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colBlue).
			MarginBottom(1)

	styleSubtitle = lipgloss.NewStyle().
			Foreground(colSubtle).
			MarginBottom(1)

	styleCard = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colBorder).
			Background(colSurface).
			Padding(1, 2).
			MarginRight(2)

	styleCardLabel = lipgloss.NewStyle().
			Foreground(colSubtle).
			Background(colSurface)

	styleCardValue = lipgloss.NewStyle().
			Bold(true).
			Foreground(colGreen).
			Background(colSurface).
			MarginTop(1)

	styleTab = lipgloss.NewStyle().
			Foreground(colSubtle).
			Padding(0, 2)

	styleTabActive = lipgloss.NewStyle().
			Bold(true).
			Foreground(colBlue).
			Padding(0, 2).
			Border(lipgloss.NormalBorder(), false, false, true, false).
			BorderForeground(colBlue)

	styleHelp = lipgloss.NewStyle().
			Foreground(colSubtle).
			MarginTop(1)

	styleSectionHeader = lipgloss.NewStyle().
				Bold(true).
				Foreground(colText).
				MarginBottom(1).
				MarginTop(1)

	styleStatusDone = lipgloss.NewStyle().
			Foreground(colGreen)

	styleStatusProgress = lipgloss.NewStyle().
				Foreground(colYellow)

	styleProto = lipgloss.NewStyle().
			Foreground(colPurple)

	styleStateOpen = lipgloss.NewStyle().
			Foreground(colGreen)

	styleStateFiltered = lipgloss.NewStyle().
				Foreground(colYellow)

	styleStateClosed = lipgloss.NewStyle().
				Foreground(colRed)

	styleBack = lipgloss.NewStyle().
			Foreground(colBlue).
			MarginBottom(1)

	tableStyle = table.DefaultStyles()
)

func init() {
	tableStyle.Header = tableStyle.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(colBorder).
		BorderBottom(true).
		Bold(true).
		Foreground(colBlue)
	tableStyle.Selected = tableStyle.Selected.
		Foreground(colText).
		Background(colSurface).
		Bold(false)
	tableStyle.Cell = tableStyle.Cell.Foreground(colText)
}

// --- view enum ---

type view int

const (
	viewDashboard view = iota
	viewHosts
	viewHostDetail
	viewScans
)

// --- messages ---

type dataLoadedMsg struct {
	hosts []*models.Host
	ports []*models.Port // only set when loading host detail
	scans []*models.Scan
	total int
	err   error
}

// --- model ---

// Model is the top-level bubbletea model.
type Model struct {
	hosts store.HostStore
	ports store.PortStore
	scans store.ScanStore

	current view
	width   int
	height  int
	loading bool
	err     error
	spin    spinner.Model

	// data
	hostList  []*models.Host
	scanList  []*models.Scan
	hostTotal int

	// detail view
	selectedHost *models.Host
	portList     []*models.Port

	// tables
	hostTable   table.Model
	scanTable   table.Model
	portTable   table.Model
	detailTable table.Model
}

// New creates a new TUI Model connected to the given stores.
func New(hosts store.HostStore, ports store.PortStore, scans store.ScanStore) Model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(colBlue)

	m := Model{
		hosts:   hosts,
		ports:   ports,
		scans:   scans,
		current: viewDashboard,
		loading: true,
		spin:    sp,
	}
	return m
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.spin.Tick, m.loadAll())
}

// --- update ---

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.rebuildTables()
		return m, nil

	case dataLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.hostList = msg.hosts
		m.scanList = msg.scans
		m.hostTotal = msg.total
		if msg.ports != nil {
			m.portList = msg.ports
		}
		m.rebuildTables()
		return m, nil

	case spinner.TickMsg:
		if m.loading {
			var cmd tea.Cmd
			m.spin, cmd = m.spin.Update(msg)
			return m, cmd
		}
		return m, nil
	}

	// route table events to the active table
	switch m.current {
	case viewHosts:
		var cmd tea.Cmd
		m.hostTable, cmd = m.hostTable.Update(msg)
		return m, cmd
	case viewScans:
		var cmd tea.Cmd
		m.scanTable, cmd = m.scanTable.Update(msg)
		return m, cmd
	case viewHostDetail:
		var cmd tea.Cmd
		m.portTable, cmd = m.portTable.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit

	case "1":
		if m.current != viewDashboard {
			m.current = viewDashboard
			m.loading = true
			return m, tea.Batch(m.spin.Tick, m.loadAll())
		}

	case "2":
		if m.current != viewHosts {
			m.current = viewHosts
			m.loading = true
			return m, tea.Batch(m.spin.Tick, m.loadAll())
		}

	case "3":
		if m.current != viewScans {
			m.current = viewScans
			m.loading = true
			return m, tea.Batch(m.spin.Tick, m.loadAll())
		}

	case "r":
		m.loading = true
		if m.current == viewHostDetail && m.selectedHost != nil {
			return m, tea.Batch(m.spin.Tick, m.loadHostDetail(m.selectedHost.IPAddress))
		}
		return m, tea.Batch(m.spin.Tick, m.loadAll())

	case "enter":
		if m.current == viewHosts && len(m.hostList) > 0 {
			row := m.hostTable.Cursor()
			if row < len(m.hostList) {
				m.selectedHost = m.hostList[row]
				m.current = viewHostDetail
				m.loading = true
				return m, tea.Batch(m.spin.Tick, m.loadHostDetail(m.selectedHost.IPAddress))
			}
		}

	case "esc", "backspace":
		if m.current == viewHostDetail {
			m.current = viewHosts
			return m, nil
		}
	}

	// pass remaining keys to the active table
	switch m.current {
	case viewHosts:
		var cmd tea.Cmd
		m.hostTable, cmd = m.hostTable.Update(msg)
		return m, cmd
	case viewScans:
		var cmd tea.Cmd
		m.scanTable, cmd = m.scanTable.Update(msg)
		return m, cmd
	case viewHostDetail:
		var cmd tea.Cmd
		m.portTable, cmd = m.portTable.Update(msg)
		return m, cmd
	}
	return m, nil
}

// --- data loading ---

func (m Model) loadAll() tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		hosts, err := m.hosts.List(ctx)
		if err != nil {
			return dataLoadedMsg{err: err}
		}
		total, err := m.hosts.Count(ctx)
		if err != nil {
			return dataLoadedMsg{err: err}
		}
		scans, err := m.scans.List(ctx)
		if err != nil {
			return dataLoadedMsg{err: err}
		}
		return dataLoadedMsg{hosts: hosts, scans: scans, total: total}
	}
}

func (m Model) loadHostDetail(ip string) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		hosts, err := m.hosts.List(ctx)
		if err != nil {
			return dataLoadedMsg{err: err}
		}
		total, err := m.hosts.Count(ctx)
		if err != nil {
			return dataLoadedMsg{err: err}
		}
		scans, err := m.scans.List(ctx)
		if err != nil {
			return dataLoadedMsg{err: err}
		}
		host, err := m.hosts.GetByIP(ctx, ip)
		if err != nil {
			return dataLoadedMsg{err: err}
		}
		ports, err := m.ports.ListByHost(ctx, host.ID)
		if err != nil {
			return dataLoadedMsg{err: err}
		}
		return dataLoadedMsg{hosts: hosts, scans: scans, total: total, ports: ports}
	}
}

// --- table builders ---

func (m *Model) rebuildTables() {
	usableH := m.height - 10 // header + tabs + help
	if usableH < 4 {
		usableH = 4
	}

	m.hostTable = buildHostTable(m.hostList, m.width, usableH)
	m.scanTable = buildScanTable(m.scanList, m.width, usableH)
	if m.selectedHost != nil {
		m.portTable = buildPortTable(m.portList, m.width, usableH-6)
	}
}

func buildHostTable(hosts []*models.Host, width, height int) table.Model {
	cols := []table.Column{
		{Title: "IP Address", Width: 16},
		{Title: "Hostname", Width: 22},
		{Title: "MAC Address", Width: 20},
		{Title: "Vendor", Width: 18},
		{Title: "Device Type", Width: 14},
		{Title: "Last Seen", Width: 20},
	}
	rows := make([]table.Row, 0, len(hosts))
	for _, h := range hosts {
		rows = append(rows, table.Row{
			h.IPAddress,
			orDash(h.Hostname),
			orDash(h.MACAddress),
			orDash(h.Vendor),
			orDash(h.DeviceType),
			fmtTime(h.LastSeen),
		})
	}
	t := table.New(
		table.WithColumns(cols),
		table.WithRows(rows),
		table.WithFocused(true),
		table.WithHeight(height),
	)
	t.SetStyles(tableStyle)
	_ = width
	return t
}

func buildScanTable(scans []*models.Scan, width, height int) table.Model {
	cols := []table.Column{
		{Title: "#", Width: 6},
		{Title: "Subnet", Width: 20},
		{Title: "Hosts", Width: 8},
		{Title: "Started", Width: 22},
		{Title: "Finished", Width: 22},
		{Title: "Duration", Width: 14},
		{Title: "Status", Width: 12},
	}
	rows := make([]table.Row, 0, len(scans))
	for _, s := range scans {
		rows = append(rows, table.Row{
			fmt.Sprint(s.ID),
			s.Subnet,
			fmt.Sprint(s.HostsFound),
			fmtTime(s.StartedAt),
			fmtTimePtr(s.FinishedAt),
			fmtDuration(s.StartedAt, s.FinishedAt),
			scanStatus(s.FinishedAt),
		})
	}
	t := table.New(
		table.WithColumns(cols),
		table.WithRows(rows),
		table.WithFocused(true),
		table.WithHeight(height),
	)
	t.SetStyles(tableStyle)
	_ = width
	return t
}

func buildPortTable(ports []*models.Port, width, height int) table.Model {
	cols := []table.Column{
		{Title: "Port", Width: 8},
		{Title: "Protocol", Width: 10},
		{Title: "Service", Width: 20},
		{Title: "State", Width: 10},
		{Title: "First Seen", Width: 22},
		{Title: "Last Seen", Width: 22},
	}
	rows := make([]table.Row, 0, len(ports))
	for _, p := range ports {
		rows = append(rows, table.Row{
			fmt.Sprint(p.Number),
			string(p.Protocol),
			orDash(p.Service),
			string(p.State),
			fmtTime(p.FirstSeen),
			fmtTime(p.LastSeen),
		})
	}
	t := table.New(
		table.WithColumns(cols),
		table.WithRows(rows),
		table.WithFocused(true),
		table.WithHeight(height),
	)
	t.SetStyles(tableStyle)
	_ = width
	return t
}

// --- view ---

func (m Model) View() string {
	if m.width == 0 {
		return "loading…"
	}

	var b strings.Builder
	b.WriteString(m.renderHeader())
	b.WriteString(m.renderTabs())
	b.WriteString("\n")

	if m.loading {
		b.WriteString("\n  " + m.spin.View() + " Loading…\n")
	} else if m.err != nil {
		b.WriteString(lipgloss.NewStyle().Foreground(colRed).Render("  Error: "+m.err.Error()) + "\n")
	} else {
		switch m.current {
		case viewDashboard:
			b.WriteString(m.renderDashboard())
		case viewHosts:
			b.WriteString(m.renderHosts())
		case viewHostDetail:
			b.WriteString(m.renderHostDetail())
		case viewScans:
			b.WriteString(m.renderScans())
		}
	}

	b.WriteString(m.renderHelp())
	return styleBase.Render(b.String())
}

func (m Model) renderHeader() string {
	return styleTitle.Render("Network Inventory Console") + "\n"
}

func (m Model) renderTabs() string {
	tabs := []struct {
		key  string
		name string
		v    view
	}{
		{"1", "Dashboard", viewDashboard},
		{"2", "Hosts", viewHosts},
		{"3", "Scans", viewScans},
	}
	var parts []string
	for _, t := range tabs {
		label := fmt.Sprintf("[%s] %s", t.key, t.name)
		if m.current == t.v || (m.current == viewHostDetail && t.v == viewHosts) {
			parts = append(parts, styleTabActive.Render(label))
		} else {
			parts = append(parts, styleTab.Render(label))
		}
	}
	return strings.Join(parts, "")
}

func (m Model) renderHelp() string {
	keys := "↑/↓: navigate  enter: select  esc: back  r: refresh  q: quit"
	return "\n" + styleHelp.Render(keys)
}

func (m Model) renderDashboard() string {
	var b strings.Builder

	// stat cards
	lastScan := "—"
	if len(m.scanList) > 0 && m.scanList[0].FinishedAt != nil {
		lastScan = fmtTime(*m.scanList[0].FinishedAt)
	}
	cards := []struct{ label, value string }{
		{"Total Hosts", fmt.Sprint(m.hostTotal)},
		{"Scans Run", fmt.Sprint(len(m.scanList))},
		{"Last Scan", lastScan},
	}
	var cardParts []string
	for _, c := range cards {
		card := styleCard.Render(
			styleCardLabel.Render(c.label) + "\n" +
				styleCardValue.Render(c.value),
		)
		cardParts = append(cardParts, card)
	}
	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, cardParts...) + "\n\n")

	// recent scans
	b.WriteString(styleSectionHeader.Render("Recent Scans") + "\n")
	recent := m.scanList
	if len(recent) > 5 {
		recent = recent[:5]
	}
	if len(recent) == 0 {
		b.WriteString(styleSubtitle.Render("  No scans recorded yet.") + "\n")
	} else {
		b.WriteString(renderScanRows(recent))
	}

	// recent hosts
	b.WriteString("\n" + styleSectionHeader.Render("Recent Hosts") + "\n")
	recentH := m.hostList
	if len(recentH) > 5 {
		recentH = recentH[:5]
	}
	if len(recentH) == 0 {
		b.WriteString(styleSubtitle.Render("  No hosts discovered yet.") + "\n")
	} else {
		b.WriteString(renderHostRows(recentH))
	}

	return b.String()
}

func renderScanRows(scans []*models.Scan) string {
	var b strings.Builder
	for _, s := range scans {
		status := styleStatusProgress.Render("in progress")
		if s.FinishedAt != nil {
			status = styleStatusDone.Render("done")
		}
		b.WriteString(fmt.Sprintf("  %-20s  hosts:%-4d  started:%-22s  %s\n",
			s.Subnet,
			s.HostsFound,
			fmtTime(s.StartedAt),
			status,
		))
	}
	return b.String()
}

func renderHostRows(hosts []*models.Host) string {
	var b strings.Builder
	for _, h := range hosts {
		b.WriteString(fmt.Sprintf("  %-16s  %-22s  %s\n",
			h.IPAddress,
			orDash(h.Hostname),
			fmtTime(h.LastSeen),
		))
	}
	return b.String()
}

func (m Model) renderHosts() string {
	var b strings.Builder
	b.WriteString(styleSubtitle.Render(fmt.Sprintf("%d hosts discovered — enter to view ports", len(m.hostList))) + "\n")
	b.WriteString(m.hostTable.View())
	return b.String()
}

func (m Model) renderHostDetail() string {
	if m.selectedHost == nil {
		return ""
	}
	h := m.selectedHost
	var b strings.Builder
	b.WriteString(styleBack.Render("← Hosts (esc)") + "\n")
	b.WriteString(styleTitle.Render(h.IPAddress) + "\n")

	meta := []struct{ k, v string }{
		{"Hostname", orDash(h.Hostname)},
		{"MAC Address", orDash(h.MACAddress)},
		{"OS Fingerprint", orDash(h.OSFingerprint)},
		{"Vendor", orDash(h.Vendor)},
		{"Device Type", orDash(h.DeviceType)},
		{"First Seen", fmtTime(h.FirstSeen)},
		{"Last Seen", fmtTime(h.LastSeen)},
	}
	for _, kv := range meta {
		b.WriteString(fmt.Sprintf("  %-18s %s\n",
			styleCardLabel.Render(kv.k+":"),
			kv.v,
		))
	}
	b.WriteString("\n")
	b.WriteString(styleSectionHeader.Render(fmt.Sprintf("Open Ports — %d found", len(m.portList))) + "\n")
	if len(m.portList) == 0 {
		b.WriteString(styleSubtitle.Render("  No ports recorded for this host.") + "\n")
	} else {
		b.WriteString(m.portTable.View())
	}
	return b.String()
}

func (m Model) renderScans() string {
	var b strings.Builder
	b.WriteString(styleSubtitle.Render(fmt.Sprintf("%d scans recorded, newest first", len(m.scanList))) + "\n")
	b.WriteString(m.scanTable.View())
	return b.String()
}

// --- helpers ---

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.UTC().Format("2006-01-02 15:04:05")
}

func fmtTimePtr(t *time.Time) string {
	if t == nil {
		return "—"
	}
	return t.UTC().Format("2006-01-02 15:04:05")
}

func fmtDuration(start time.Time, end *time.Time) string {
	if end == nil {
		return "in progress"
	}
	return end.Sub(start).Round(time.Second).String()
}

func scanStatus(finishedAt *time.Time) string {
	if finishedAt == nil {
		return "in progress"
	}
	return "done"
}
