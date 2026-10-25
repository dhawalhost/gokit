package i18n

import (
	"strings"
)

// PluralCategory represents the standard CLDR plural category.
type PluralCategory string

const (
	// PluralZero represents the zero category (used in Arabic, etc.).
	PluralZero PluralCategory = "zero"
	// PluralOne represents the singular category.
	PluralOne PluralCategory = "one"
	// PluralTwo represents the dual category (used in Arabic, Welsh, etc.).
	PluralTwo PluralCategory = "two"
	// PluralFew represents the paucal category (used in Russian, Polish, etc.).
	PluralFew PluralCategory = "few"
	// PluralMany represents the plural category for larger counts.
	PluralMany PluralCategory = "many"
	// PluralOther represents the default fallback category for all other numbers.
	PluralOther PluralCategory = "other"
)

// PluralRule computes the CLDR PluralCategory for a given numeric count.
type PluralRule func(count int) PluralCategory

// SelectPlural returns the appropriate PluralCategory for a given language code and integer count.
func SelectPlural(lang string, count int) PluralCategory {
	lang = strings.ToLower(strings.Split(lang, "-")[0])

	switch lang {
	case "ja", "zh", "ko", "vi", "th", "id", "ms":
		// Asian / Austronesian languages with no plural inflection
		return PluralOther

	case "fr", "pt_br", "pt-br":
		// French: 0 and 1 are singular
		abs := absInt(count)
		if abs == 0 || abs == 1 {
			return PluralOne
		}
		return PluralOther

	case "ru", "uk", "be":
		// Russian, Ukrainian, Belarusian
		abs := absInt(count)
		rem10 := abs % 10
		rem100 := abs % 100
		if rem10 == 1 && rem100 != 11 {
			return PluralOne
		}
		if rem10 >= 2 && rem10 <= 4 && (rem100 < 12 || rem100 > 14) {
			return PluralFew
		}
		if rem10 == 0 || (rem10 >= 5 && rem10 <= 9) || (rem100 >= 11 && rem100 <= 14) {
			return PluralMany
		}
		return PluralOther

	case "pl":
		// Polish
		abs := absInt(count)
		if abs == 1 {
			return PluralOne
		}
		rem10 := abs % 10
		rem100 := abs % 100
		if rem10 >= 2 && rem10 <= 4 && (rem100 < 12 || rem100 > 14) {
			return PluralFew
		}
		return PluralMany

	case "ar":
		// Arabic (6 forms)
		abs := absInt(count)
		if abs == 0 {
			return PluralZero
		}
		if abs == 1 {
			return PluralOne
		}
		if abs == 2 {
			return PluralTwo
		}
		rem100 := abs % 100
		if rem100 >= 3 && rem100 <= 10 {
			return PluralFew
		}
		if rem100 >= 11 && rem100 <= 99 {
			return PluralMany
		}
		return PluralOther

	default:
		// Default rule (English, German, Spanish, Italian, etc.): 1 is singular, everything else is other
		if absInt(count) == 1 {
			return PluralOne
		}
		return PluralOther
	}
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
