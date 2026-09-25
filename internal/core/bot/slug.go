package bot

import "strings"

// Names come in the language the operator thinks in, and ids have to come out of
// them anyway: a client id, a group id, the login a config is issued under. The
// dropping every letter it does not recognise would turn "Пётр Ильин" into
// "u-user-8c31d0": an id with nothing of the name left in it, in a list where
// the id is how you find the record again.
//
// So Cyrillic is transliterated and not discarded. The table is the one the
// Russian post office and every transport form uses; it is not reversible and
// does not need to be, because the name itself is stored next to the id.
var cyrillic = map[rune]string{
	'а': "a", 'б': "b", 'в': "v", 'г': "g", 'д': "d", 'е': "e", 'ё': "e",
	'ж': "zh", 'з': "z", 'и': "i", 'й': "i", 'к': "k", 'л': "l", 'м': "m",
	'н': "n", 'о': "o", 'п': "p", 'р': "r", 'с': "s", 'т': "t", 'у': "u",
	'ф': "f", 'х': "kh", 'ц': "ts", 'ч': "ch", 'ш': "sh", 'щ': "shch",
	'ъ': "", 'ы': "y", 'ь': "", 'э': "e", 'ю': "iu", 'я': "ia",
	// Ukrainian and Belarusian letters that share the keyboard.
	'і': "i", 'ї': "i", 'є': "e", 'ґ': "g", 'ў': "u",
}

// slugify turns a name into the safe part of an id: lower-case Latin letters,
// digits and single dashes.
func slugify(name string) string {
	var sb strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			sb.WriteRune(r)
		case r == ' ' || r == '-' || r == '_' || r == '.':
			sb.WriteByte('-')
		default:
			if s, ok := cyrillic[r]; ok {
				sb.WriteString(s)
			}
		}
	}
	// Two words separated by something dropped on the way would otherwise run
	// together as one.
	out := sb.String()
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	return strings.Trim(out, "-")
}
