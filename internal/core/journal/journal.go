// Package journal writes the panel's event log in the language it is read in.
//
// A line is built where the thing happens, out of an English sentence and the
// values that fill it in, and it is rendered twice: the English text, which is
// what the table has always held and what anything reading it directly still
// sees, and the Russian one an operator reads in the panel. Both are written at
// the moment of the event, because a sentence already flattened into a row
// cannot be translated afterwards: the names, counts and reasons inside it are
// no longer separable from the words around them.
//
// The same idea as the alert vocabulary in internal/core/watchdog: English is
// the source and the key, a missing translation degrades to English instead of
// to a blank.
package journal

import (
	"fmt"
	"strconv"
	"strings"
)

// Lang is the source language: the key every translation is looked up by, and
// what a reader whose language we do not know gets.
const Lang = "en"

// Msg renders one line, or one value inside a line, in the given language.
type Msg func(lang string) string

// Line builds a message from an English sentence and its arguments. An argument
// that is itself a Msg (a counted noun, a term with a translation of its own) is
// rendered in the language of the sentence around it, so word order and
// agreement stay inside the translation instead of leaking to the call site.
func Line(en string, args ...any) Msg {
	return func(lang string) string {
		f := tr(lang, en)
		if len(args) == 0 {
			return f
		}
		out := make([]any, len(args))
		for i, a := range args {
			if m, ok := a.(Msg); ok {
				out[i] = m(lang)
			} else {
				out[i] = a
			}
		}
		return fmt.Sprintf(f, out...)
	}
}

// Join puts lines end to end. A sentence that only sometimes has a tail (a count
// of what else the same failure took with it) is built from two pieces rather
// than from a format with an empty hole in it.
func Join(parts ...Msg) Msg {
	return func(lang string) string {
		var b strings.Builder
		for _, p := range parts {
			if p != nil {
				b.WriteString(p(lang))
			}
		}
		return b.String()
	}
}

// Text is a value that reads the same in every language: a name, an id, an
// error from somewhere else.
func Text(s string) Msg { return func(string) string { return s } }

// Both is a line whose two renderings are already in hand: an alert, which the
// alert vocabulary has translated on its way to Telegram.
func Both(en, ru string) Msg {
	return func(lang string) string {
		if lang == "ru" && ru != "" {
			return ru
		}
		return en
	}
}

// Term translates a single word that arrives as data (a role, a kind of record,
// the name of a bulk action) and leaves anything it does not know exactly as it
// came.
func Term(s string) Msg {
	return func(lang string) string {
		if lang == "ru" {
			if v, ok := terms[s]; ok {
				return v
			}
		}
		return s
	}
}

// And joins a list the way the sentence around it would say it.
func And(items []string) Msg {
	return func(lang string) string {
		w := " and "
		if lang == "ru" {
			w = " и "
		}
		return strings.Join(items, w)
	}
}

// Count is a number the English sentence carries its noun next to ("one group",
// "three groups") and the Russian sentence carries bare. In Russian the noun
// beside a numeral changes form with the number and the verb changes with it in
// turn, so a noun dropped in next to the count reads wrong for one count or
// another however it is spelled. The Russian sentences here name the thing they
// are counting themselves and end with the number, which is right for every
// count there is.
func Count(n int, enOne, enMany string) Msg {
	return func(lang string) string {
		switch {
		case lang == "ru":
			return strconv.Itoa(n)
		case n == 1:
			return fmt.Sprintf("%d %s", n, enOne)
		}
		return fmt.Sprintf("%d %s", n, enMany)
	}
}

// Groups, Rules and Clients are the counted things the log needs more than once.
func Groups(n int) Msg  { return Count(n, "group", "groups") }
func Rules(n int) Msg   { return Count(n, "rule", "rules") }
func Clients(n int) Msg { return Count(n, "client", "clients") }

// Render is nil-safe: a line that was never built renders as nothing.
func Render(m Msg, lang string) string {
	if m == nil {
		return ""
	}
	return m(lang)
}

// tr returns the sentence for lang: the Russian translation when there is one,
// else the English source, which doubles as the key.
func tr(lang, en string) string {
	if lang == "ru" {
		if v, ok := ru[en]; ok {
			return v
		}
	}
	return en
}
