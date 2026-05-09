// Package render holds testagent's intent-named styling tokens, inspired
// by Catppuccin Mocha.
//
// testagent's deployed canvas today is a dark terminal (the demo/hero.tape
// runs `Set Theme "Catppuccin Mocha"`), so foreground colors are picked
// from the Mocha palette. The tokens are organized by intent — what the
// styled bytes mean — not by hue:
//
//   - Warm hues  (peach, maroon, flamingo, yellow, red) → things the agent
//     produced or that need attention: session identity, agent reply,
//     settled thoughts, hook warnings, errors.
//   - Cool hues  (sapphire, sky, blue, green) → machine state and chrome:
//     banner border/meta, prompt cue, tool calls, success marks.
//   - Neutrals   (overlay/subtext) → ambient wiring: spinner, lifecycle
//     notes, panel border, help usage.
//
// Borders carry semantics too: double = session chrome (banner only),
// rounded = data container (panel).
//
// Verbose hook traces (internal/hooks) are intentionally plain text per
// AGENTS.md and do not import this package.
package render

import "github.com/charmbracelet/lipgloss"

// Catppuccin Mocha foreground hex values.
// https://github.com/catppuccin/catppuccin (mocha flavor).
const (
	mochaPeach    = "#fab387"
	mochaSapphire = "#74c7ec"
	mochaSky      = "#89dceb"
	mochaGreen    = "#a6e3a1"
	mochaBlue     = "#89b4fa"
	mochaMaroon   = "#eba0ac"
	mochaFlamingo = "#f2cdcd"
	mochaYellow   = "#f9e2af"
	mochaRed      = "#f38ba8"
	mochaOverlay1 = "#7f849c"
	mochaOverlay0 = "#6c7086"
	mochaSubtext0 = "#a6adc8"
)

// Intent tokens. Foreground/decoration only — no backgrounds. Exported as
// vars so external callers can chain (.Faint(true).Render(...)) and assign
// to spinner styles; the value-style helpers below cover the common cases.
var (
	SessionStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color(mochaPeach)).Bold(true)
	BannerMetaStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(mochaSky))
	okStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color(mochaGreen)).Bold(true)
	ToolStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color(mochaBlue)).Bold(true)
	echoStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color(mochaMaroon)).Bold(true)
	warnStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color(mochaYellow))
	ErrorStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color(mochaRed)).Bold(true)
	MuteStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color(mochaOverlay1))
	MuteSoftStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color(mochaSubtext0))
	// ThinkingStyle marks the in-flight "Thinking…" label — warm flamingo
	// italic, suggesting active mental work.
	ThinkingStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(mochaFlamingo)).Italic(true)
	// ThoughtMarkerStyle marks the settled "Thought for Ns" line — muted
	// italic, since it's a past artifact in scrollback. Same italic family
	// as ThinkingStyle but cooler so completed turns recede visually.
	ThoughtMarkerStyle = MuteStyle.Italic(true)

	PanelStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(mochaOverlay0)).
			Padding(0, 1)

	BannerStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.DoubleBorder()).
			BorderForeground(lipgloss.Color(mochaSapphire)).
			Padding(0, 2)
)

// Intent-named render helpers — call sites read as the intent, not the style.

func Prompt() string                        { return okStyle.Render("> ") }
func Echo(name, msg string) string          { return EchoHeader(name) + " " + msg }
func EchoHeader(name string) string          { return echoStyle.Render("[" + name + "]") }
func Lifecycle(s string) string             { return MuteStyle.Render("[" + s + "]") }
func LifecycleWarn(s string) string         { return warnStyle.Render("[" + s + "]") }
func ToolHeader(prefix, name string) string { return ToolStyle.Render(prefix + name) }
func ResultOk() string                      { return okStyle.Render("✓") }
func ResultErr() string                     { return ErrorStyle.Render("✗") }
func Thinking(s string) string              { return ThinkingStyle.Render(s) }
func ThoughtMarker(s string) string         { return ThoughtMarkerStyle.Render(s) }
