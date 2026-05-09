// Intent-named styling tokens, inspired by Catppuccin Mocha.
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
// Verbose hook traces (hooks.go) are intentionally plain text per AGENTS.md
// and do not import this file.
package main

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

// Intent tokens. Foreground/decoration only — no backgrounds.
var (
	accentSession = lipgloss.NewStyle().Foreground(lipgloss.Color(mochaPeach)).Bold(true)
	bannerMeta    = lipgloss.NewStyle().Foreground(lipgloss.Color(mochaSky))
	accentOk      = lipgloss.NewStyle().Foreground(lipgloss.Color(mochaGreen)).Bold(true)
	accentTool    = lipgloss.NewStyle().Foreground(lipgloss.Color(mochaBlue)).Bold(true)
	accentEcho    = lipgloss.NewStyle().Foreground(lipgloss.Color(mochaMaroon)).Bold(true)
	accentWarn    = lipgloss.NewStyle().Foreground(lipgloss.Color(mochaYellow))
	accentErr     = lipgloss.NewStyle().Foreground(lipgloss.Color(mochaRed)).Bold(true)
	mute          = lipgloss.NewStyle().Foreground(lipgloss.Color(mochaOverlay1))
	muteSoft      = lipgloss.NewStyle().Foreground(lipgloss.Color(mochaSubtext0))
	// thinking marks the in-flight "Thinking…" label — warm flamingo
	// italic, suggesting active mental work.
	thinking = lipgloss.NewStyle().Foreground(lipgloss.Color(mochaFlamingo)).Italic(true)
	// thoughtMarker marks the settled "Thought for Ns" line — muted italic,
	// since it's a past artifact in scrollback. Same italic family as
	// thinking but cooler so completed turns recede visually.
	thoughtMarker = mute.Italic(true)

	stylePanel = lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(mochaOverlay0)).
			Padding(0, 1)

	styleBanner = lipgloss.NewStyle().
			BorderStyle(lipgloss.DoubleBorder()).
			BorderForeground(lipgloss.Color(mochaSapphire)).
			Padding(0, 2)
)

// Intent-named render helpers — call sites read as the intent, not the style.

func renderPrompt() string                        { return accentOk.Render("> ") }
func renderEcho(name, msg string) string          { return accentEcho.Render("["+name+"]") + " " + msg }
func renderLifecycle(s string) string             { return mute.Render("[" + s + "]") }
func renderLifecycleWarn(s string) string         { return accentWarn.Render("[" + s + "]") }
func renderToolHeader(prefix, name string) string { return accentTool.Render(prefix + name) }
func renderResultOk() string                      { return accentOk.Render("✓") }
func renderResultErr() string                     { return accentErr.Render("✗") }
func renderThinking(s string) string              { return thinking.Render(s) }
func renderThoughtMarker(s string) string         { return thoughtMarker.Render(s) }
