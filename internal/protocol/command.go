package protocol

import "strings"

// A plugin has no inbound channel. It cannot serve HTTP, register a route, or
// expose an endpoint of any kind, so there is no way for a client to call it.
//
// The way round that is a playlist used as a mailbox: a client writes a
// command into the control playlist's description, and the plugin reads it on
// its next poll. That makes the command grammar part of the wire format, and
// it has to be as defensive as the description parser, because anything at all
// can end up in a field a user can edit by hand.

// CommandKind is the verb of a control command.
type CommandKind string

const (
	// CmdReroll asks for one mix to be regenerated now.
	CmdReroll CommandKind = "reroll"
	// CmdRefreshAll asks for every mix to be regenerated.
	CmdRefreshAll CommandKind = "refresh"
)

// Command is a request from a client.
type Command struct {
	Kind CommandKind
	// Slot is the mix the command applies to. Empty for commands that do not
	// target one.
	Slot string
	// Nonce makes a command unique. Without it, a command left sitting in the
	// playlist would be executed again on every single poll.
	Nonce string
}

const commandFields = 5

// FormatCommand renders a command line, which is what a client writes.
//
//	nb1:cmd:reroll:morning:8f21
func FormatCommand(c Command) string {
	return strings.Join([]string{Version, "cmd", string(c.Kind), c.Slot, c.Nonce}, ":")
}

// ParseCommand extracts a command from a control playlist description.
//
// Returns ok=false for anything it does not fully understand. A malformed
// command must be ignored and cleared rather than guessed at: acting on half a
// parsed instruction is worse than doing nothing, because the user cannot see
// what the plugin thought it read.
func ParseCommand(comment string) (Command, bool) {
	for _, line := range strings.Split(comment, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, Version+":cmd:") {
			continue
		}
		parts := strings.Split(line, ":")
		if len(parts) != commandFields {
			return Command{}, false
		}
		kind := CommandKind(parts[2])
		if kind != CmdReroll && kind != CmdRefreshAll {
			return Command{}, false
		}
		if parts[4] == "" {
			// No nonce means it would run again on every poll.
			return Command{}, false
		}
		return Command{Kind: kind, Slot: parts[3], Nonce: parts[4]}, true
	}
	return Command{}, false
}

// ResultKind is the outcome the plugin writes back.
type ResultKind string

const (
	ResultDone     ResultKind = "done"
	ResultRejected ResultKind = "rejected"
)

// FormatResult renders the line the plugin writes back into the control
// playlist so the client can tell its command was seen.
//
//	nb1:res:done:morning:8f21
func FormatResult(kind ResultKind, slot, nonce string) string {
	return strings.Join([]string{Version, "res", string(kind), slot, nonce}, ":")
}

// ParseResult reads a result line, which is what a client polls for.
func ParseResult(comment string) (kind ResultKind, slot, nonce string, ok bool) {
	for _, line := range strings.Split(comment, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, Version+":res:") {
			continue
		}
		parts := strings.Split(line, ":")
		if len(parts) != commandFields {
			return "", "", "", false
		}
		return ResultKind(parts[2]), parts[3], parts[4], true
	}
	return "", "", "", false
}
