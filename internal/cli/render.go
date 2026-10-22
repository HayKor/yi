package cli

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/HayKor/yi/internal/config"
	"github.com/HayKor/yi/internal/domain"
	"github.com/HayKor/yi/internal/reconcile"
)

// ANSI color codes used by renderStatus; see spec §6.6.
const (
	ansiReset  = "\033[0m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiRed    = "\033[31m"
)

// renderStatus writes the `yi status` table to w (Issue, Local,
// Remote, Title, State columns, tab-aligned), followed by any
// warnings as "warning: <msg>" lines. color enables ANSI color on the
// state marker; the caller decides whether that's appropriate (TTY
// and NO_COLOR unset).
func renderStatus(w io.Writer, cfg *config.Config, rows []reconcile.Row, warns []string, color bool) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "Issue\tLocal\tRemote\tTitle\tState")

	for _, row := range rows {
		issue := "-"
		if row.ID != 0 {
			issue = fmt.Sprintf("#%d", row.ID)
		}

		marker := stateMarker(cfg, row)
		if color {
			marker = colorizeMarker(marker, row.Kind)
		}

		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			issue, headingOrDash(cfg, row.Local), headingOrDash(cfg, row.Remote), row.Title, marker)
	}
	tw.Flush()

	for _, msg := range warns {
		fmt.Fprintf(w, "warning: %s\n", msg)
	}
}

// stateMarker renders row's State column: the kind's own marker, plus
// " (open)" when the matched remote issue is open despite being
// mapped to a closeIssue column (it's labeled into the done column
// without actually being closed yet; spec §6.5/§6.6).
func stateMarker(cfg *config.Config, row reconcile.Row) string {
	var marker string
	switch row.Kind {
	case reconcile.RowOK:
		marker = "✓"
	case reconcile.RowDiff:
		marker = "!"
	case reconcile.RowLocalOnly:
		marker = "+local"
	case reconcile.RowRemoteOnly:
		marker = "+remote"
	case reconcile.RowMissing:
		marker = "?"
	}

	if row.Remote != "" && row.RemoteOpen {
		if st, ok := cfg.Status(row.Remote); ok && st.CloseIssue {
			marker += " (open)"
		}
	}
	return marker
}

// colorizeMarker wraps marker in the ANSI color for kind: green for
// RowOK, yellow for RowDiff, red for RowMissing. RowLocalOnly and
// RowRemoteOnly are left uncolored.
func colorizeMarker(marker string, kind reconcile.RowKind) string {
	switch kind {
	case reconcile.RowOK:
		return ansiGreen + marker + ansiReset
	case reconcile.RowDiff:
		return ansiYellow + marker + ansiReset
	case reconcile.RowMissing:
		return ansiRed + marker + ansiReset
	default:
		return marker
	}
}

// headingOrDash renders a status key as its configured heading, or
// "-" if key is empty (unknown/not applicable).
func headingOrDash(cfg *config.Config, key domain.StatusKey) string {
	if key == "" {
		return "-"
	}
	if s, ok := cfg.Status(key); ok {
		return s.Heading
	}
	return string(key)
}

// renderPlan writes a Plan to w: "Changes:" followed by one indented
// line per change (via reconcile.Describe), or "No changes." if the
// plan is empty; then any warnings as "warning: <msg>" lines.
func renderPlan(w io.Writer, cfg *config.Config, p reconcile.Plan) {
	if p.Empty() {
		fmt.Fprintln(w, "No changes.")
	} else {
		fmt.Fprintln(w, "Changes:")
		for _, c := range p.Changes {
			fmt.Fprintf(w, "  %s\n", reconcile.Describe(cfg, c))
		}
	}

	for _, msg := range p.Warnings {
		fmt.Fprintf(w, "warning: %s\n", msg)
	}
}
