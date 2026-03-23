# Linker CLI Design System

## Overview

This document defines the UX and UI rules for the `linker` CLI. The visual language is derived from the logo assets in `assets/logo/` and should stay consistent across onboarding, provider setup, and the future full-screen TUI.

## Brand Palette

The logo artwork establishes the canonical palette. Treat these values as the source of truth for docs, Lipgloss styles, and any terminal surfaces that represent the Linker brand.

| Token | Hex | Role |
| :--- | :--- | :--- |
| `brand.bg` | `#060C18` | Deep background canvas and full-screen shell |
| `brand.surface` | `#0C1222` | Primary panels, cards, and modal bodies |
| `brand.surface.elevated` | `#141D31` | Raised sections, previews, and focused dialogs |
| `brand.border` | `#243656` | Borders, dividers, and inactive outlines |
| `brand.cyan` | `#00DCFF` | Primary brand color, links, and active selection |
| `brand.mint` | `#50F5A0` | Success states, completed auth, and connected health |
| `brand.amber` | `#FFCB5B` | Warnings, caution states, and onboarding emphasis |
| `brand.violet` | `#A47CFF` | Secondary accent, advanced controls, and special modes |
| `brand.text` | `#EEF5FF` | Primary text on dark surfaces |
| `brand.muted` | `#99A8C2` | Secondary labels, hints, and inactive metadata |
| `brand.error` | `#FF6A6A` | Validation errors, cancellations, and destructive states |

## Semantic Mapping

Use the palette through semantic tokens rather than raw ad hoc colors.

| Semantic Name | Token | Usage |
| :--- | :--- | :--- |
| Primary / Brand | `brand.cyan` | Titles, active selection borders, highlights, and the "link" motif |
| Success | `brand.mint` | Successful operations, completed auth, and apply confirmations |
| Warning / Alert | `brand.amber` | Non-critical warnings, existing state, and override notices |
| Error | `brand.error` | Critical failures, invalid input, and cancellation notices |
| Muted / Secondary | `brand.muted` | Secondary labels, helper text, and fallback state |
| Accent | `brand.violet` | Special features, provider-specific toggles, and advanced mode cues |
| Background | `brand.bg` | Window background and empty canvas surfaces |
| Surface | `brand.surface` | Standard container backgrounds |
| Surface Elevated | `brand.surface.elevated` | Wizard cards, diffs, and preview panes |
| Border | `brand.border` | Box outlines and separators |

## TUI Framework Stack

Linker standardizes on the Charmbracelet stack for its terminal UI:

- [`charmbracelet/bubbletea`](https://github.com/charmbracelet/bubbletea): Base runtime for interactive flows and full-screen views.
- [`charmbracelet/huh`](https://github.com/charmbracelet/huh): Forms, inputs, selects, and confirmations.
- [`charmbracelet/lipgloss`](https://github.com/charmbracelet/lipgloss): Borders, spacing, and text color styling.
- [`charmbracelet/huh/spinner`](https://github.com/charmbracelet/huh/spinner): Loading and callback wait states.

## UX Principles

- Interactivity over typing: prefer interactive menus instead of asking users to enter numeric IDs or codes manually.
- Keyboard-first selection: support instant-select with numeric keys so common choices do not require an extra `Enter`.
- Clear feedback: use spinners for network I/O and color-coded status text for success, warnings, and errors.
- Visual hierarchy: use bold headers, consistent indentation, and bordered sections to guide the eye.
- Snappy transitions: keep each step reactive and avoid long dead ends between decisions.

## Onboarding Flow

- Bootstrap-only: the default path should get a user from a fresh install to a working daemon with the fewest prompts possible.
- Configure providers: provider setup should make it obvious which accounts are OAuth-based and which ones use API keys.
- Configure models: the wizard should refresh the catalog and map the `Default`, `Opus`, `Sonnet`, and `Haiku` slots from discovered models.
- Confirm before write: show the Claude settings preview before mutating the local config file.

## Component Guidelines

### Interactive Menus

- Menus should display a numeric index next to each option, such as `[1] Option A`.
- Pressing `1` should trigger the same action as highlighting `Option A` and pressing `Enter`.
- Use a clear active state, such as a prefix or border color change, to keep the current selection obvious.

### Full-Screen Dashboard

- Layout: use a paneled layout with a header, main content area, and footer.
- Navigation: use tabs or a sidebar for `Status`, `Providers`, `Logs`, and `Settings`.
- Responsiveness: adapt to terminal resizing through `bubbletea` `WindowSizeMsg`.
- Status view: show daemon health, uptime, and resource usage.
- Providers view: show active providers and account health.
- Logs view: show real-time streaming output with level-based coloring.
- Settings view: provide an interactive configuration editor.

### Loading States

- Always show a spinner when the CLI is waiting on network I/O or a localhost callback.
- Pair the spinner with a descriptive message, such as `Waiting for Google login...`.
