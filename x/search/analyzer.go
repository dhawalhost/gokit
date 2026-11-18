package search

import (
	"strings"
	"unicode"
)

// Token represents a single analyzed term with position and offset metadata.
type Token struct {
	// Text is the normalized string value of the token.
	Text string `json:"text"`

	// Position is the 0-indexed word position in the original text.
	Position int `json:"position"`

	// StartOffset is the start character index in the original text.
	StartOffset int `json:"start_offset"`

	// EndOffset is the end character index in the original text.
	EndOffset int `json:"end_offset"`
}

// Tokenizer breaks a raw string into initial tokens.
type Tokenizer interface {
	Tokenize(text string) []*Token
}

// TokenFilter modifies, adds, or drops tokens from a token stream.
type TokenFilter interface {
	Filter(tokens []*Token) []*Token
}

// TokenFilterFunc is an adapter to allow the use of ordinary functions as TokenFilters.
type TokenFilterFunc func([]*Token) []*Token

// Filter calls f(tokens).
func (f TokenFilterFunc) Filter(tokens []*Token) []*Token {
	return f(tokens)
}

// Analyzer combines a Tokenizer with an ordered pipeline of TokenFilters.
type Analyzer struct {
	Tokenizer Tokenizer
	Filters   []TokenFilter
}

// Analyze runs the tokenizer and all filters in sequence on the input text.
func (a *Analyzer) Analyze(text string) []*Token {
	if a == nil || a.Tokenizer == nil {
		return NewStandardTokenizer().Tokenize(text)
	}
	tokens := a.Tokenizer.Tokenize(text)
	for _, f := range a.Filters {
		tokens = f.Filter(tokens)
	}
	return tokens
}

// StandardTokenizer splits text on whitespace and punctuation, recording positions.
type StandardTokenizer struct{}

// NewStandardTokenizer creates a new StandardTokenizer.
func NewStandardTokenizer() *StandardTokenizer {
	return &StandardTokenizer{}
}

// Tokenize segments text by unicode punctuation and whitespace boundaries.
func (t *StandardTokenizer) Tokenize(text string) []*Token {
	var tokens []*Token
	runes := []rune(text)
	pos := 0
	inWord := false
	start := 0

	for i, r := range runes {
		isDelimiter := unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r)
		if !isDelimiter {
			if !inWord {
				inWord = true
				start = i
			}
		} else {
			if inWord {
				tokens = append(tokens, &Token{
					Text:        string(runes[start:i]),
					Position:    pos,
					StartOffset: start,
					EndOffset:   i,
				})
				pos++
				inWord = false
			}
		}
	}
	if inWord {
		tokens = append(tokens, &Token{
			Text:        string(runes[start:]),
			Position:    pos,
			StartOffset: start,
			EndOffset:   len(runes),
		})
	}
	return tokens
}

// WhitespaceTokenizer splits text solely on unicode whitespace.
type WhitespaceTokenizer struct{}

// NewWhitespaceTokenizer creates a new WhitespaceTokenizer.
func NewWhitespaceTokenizer() *WhitespaceTokenizer {
	return &WhitespaceTokenizer{}
}

// Tokenize splits on unicode spaces.
func (t *WhitespaceTokenizer) Tokenize(text string) []*Token {
	var tokens []*Token
	runes := []rune(text)
	pos := 0
	inWord := false
	start := 0

	for i, r := range runes {
		if !unicode.IsSpace(r) {
			if !inWord {
				inWord = true
				start = i
			}
		} else {
			if inWord {
				tokens = append(tokens, &Token{
					Text:        string(runes[start:i]),
					Position:    pos,
					StartOffset: start,
					EndOffset:   i,
				})
				pos++
				inWord = false
			}
		}
	}
	if inWord {
		tokens = append(tokens, &Token{
			Text:        string(runes[start:]),
			Position:    pos,
			StartOffset: start,
			EndOffset:   len(runes),
		})
	}
	return tokens
}

// EdgeNGramTokenizer generates prefix n-grams for fast autocomplete and typeahead search.
type EdgeNGramTokenizer struct {
	MinGram int
	MaxGram int
}

// NewEdgeNGramTokenizer creates an EdgeNGramTokenizer with given min/max bounds.
func NewEdgeNGramTokenizer(minGram, maxGram int) *EdgeNGramTokenizer {
	if minGram < 1 {
		minGram = 1
	}
	if maxGram < minGram {
		maxGram = minGram
	}
	return &EdgeNGramTokenizer{
		MinGram: minGram,
		MaxGram: maxGram,
	}
}

// Tokenize produces edge n-grams for each word in text.
func (t *EdgeNGramTokenizer) Tokenize(text string) []*Token {
	baseTokenizer := NewStandardTokenizer()
	baseTokens := baseTokenizer.Tokenize(text)
	var result []*Token

	pos := 0
	for _, tok := range baseTokens {
		runes := []rune(tok.Text)
		wordLen := len(runes)
		for gramLen := t.MinGram; gramLen <= t.MaxGram && gramLen <= wordLen; gramLen++ {
			gramText := string(runes[:gramLen])
			result = append(result, &Token{
				Text:        gramText,
				Position:    pos,
				StartOffset: tok.StartOffset,
				EndOffset:   tok.StartOffset + gramLen,
			})
			pos++
		}
	}
	return result
}

// LowercaseFilter converts all token text to lowercase.
func LowercaseFilter() TokenFilter {
	return TokenFilterFunc(func(tokens []*Token) []*Token {
		for _, tok := range tokens {
			tok.Text = strings.ToLower(tok.Text)
		}
		return tokens
	})
}

// ASCIIFoldFilter normalizes accented/diacritic characters to basic ASCII.
func ASCIIFoldFilter() TokenFilter {
	return TokenFilterFunc(func(tokens []*Token) []*Token {
		for _, tok := range tokens {
			var b strings.Builder
			for _, r := range tok.Text {
				switch r {
				case 'à', 'á', 'â', 'ã', 'ä', 'å', 'ā':
					b.WriteRune('a')
				case 'è', 'é', 'ê', 'ë', 'ē', 'ė', 'ę':
					b.WriteRune('e')
				case 'ì', 'í', 'î', 'ï', 'ī', 'į':
					b.WriteRune('i')
				case 'ò', 'ó', 'ô', 'õ', 'ö', 'ø', 'ō':
					b.WriteRune('o')
				case 'ù', 'ú', 'û', 'ü', 'ū':
					b.WriteRune('u')
				case 'ñ':
					b.WriteRune('n')
				case 'ç', 'ć', 'č':
					b.WriteRune('c')
				case 'ß':
					b.WriteString("ss")
				default:
					b.WriteRune(r)
				}
			}
			tok.Text = b.String()
		}
		return tokens
	})
}

// DefaultStopWords list for English.
var DefaultStopWords = map[string]struct{}{
	"a": {}, "about": {}, "above": {}, "after": {}, "again": {}, "against": {}, "all": {}, "am": {},
	"an": {}, "and": {}, "any": {}, "are": {}, "as": {}, "at": {}, "be": {}, "because": {}, "been": {},
	"before": {}, "being": {}, "below": {}, "between": {}, "both": {}, "but": {}, "by": {}, "could": {},
	"did": {}, "do": {}, "does": {}, "doing": {}, "down": {}, "during": {}, "each": {}, "few": {},
	"for": {}, "from": {}, "further": {}, "had": {}, "has": {}, "have": {}, "having": {}, "he": {},
	"her": {}, "here": {}, "hers": {}, "herself": {}, "him": {}, "himself": {}, "his": {}, "how": {},
	"i": {}, "if": {}, "in": {}, "into": {}, "is": {}, "it": {}, "its": {}, "itself": {}, "just": {},
	"me": {}, "more": {}, "most": {}, "my": {}, "myself": {}, "no": {}, "nor": {}, "not": {}, "of": {},
	"off": {}, "on": {}, "once": {}, "only": {}, "or": {}, "other": {}, "our": {}, "ours": {},
	"ourselves": {}, "out": {}, "over": {}, "own": {}, "same": {}, "she": {}, "should": {}, "so": {},
	"some": {}, "such": {}, "than": {}, "that": {}, "the": {}, "their": {}, "theirs": {}, "them": {},
	"themselves": {}, "then": {}, "there": {}, "these": {}, "they": {}, "this": {}, "those": {},
	"through": {}, "to": {}, "too": {}, "under": {}, "until": {}, "up": {}, "very": {}, "was": {},
	"we": {}, "were": {}, "what": {}, "when": {}, "where": {}, "which": {}, "while": {}, "who": {},
	"whom": {}, "why": {}, "with": {}, "would": {}, "you": {}, "your": {}, "yours": {},
}

// StopWordsFilter discards common words using a lookup map.
func StopWordsFilter(stopWords map[string]struct{}) TokenFilter {
	if stopWords == nil {
		stopWords = DefaultStopWords
	}
	return TokenFilterFunc(func(tokens []*Token) []*Token {
		filtered := make([]*Token, 0, len(tokens))
		for _, tok := range tokens {
			if _, isStop := stopWords[tok.Text]; !isStop {
				filtered = append(filtered, tok)
			}
		}
		return filtered
	})
}

// MinLengthFilter drops tokens with character length strictly less than minLen.
func MinLengthFilter(minLen int) TokenFilter {
	return TokenFilterFunc(func(tokens []*Token) []*Token {
		filtered := make([]*Token, 0, len(tokens))
		for _, tok := range tokens {
			if len([]rune(tok.Text)) >= minLen {
				filtered = append(filtered, tok)
			}
		}
		return filtered
	})
}

// StemmerFilter implements lightweight suffix stemming for English words.
func StemmerFilter() TokenFilter {
	return TokenFilterFunc(func(tokens []*Token) []*Token {
		for _, tok := range tokens {
			tok.Text = simpleStem(tok.Text)
		}
		return tokens
	})
}

func simpleStem(w string) string {
	if len(w) <= 3 {
		return w
	}
	// Plural / verb endings
	if strings.HasSuffix(w, "ies") && len(w) > 4 {
		return w[:len(w)-3] + "y"
	}
	if strings.HasSuffix(w, "ing") && len(w) > 4 {
		return unDouble(strings.TrimSuffix(w, "ing"))
	}
	if strings.HasSuffix(w, "ed") && len(w) > 3 {
		return unDouble(strings.TrimSuffix(w, "ed"))
	}
	if strings.HasSuffix(w, "ly") && len(w) > 3 {
		return strings.TrimSuffix(w, "ly")
	}
	if strings.HasSuffix(w, "es") && len(w) > 3 {
		return strings.TrimSuffix(w, "es")
	}
	if strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") && len(w) > 2 {
		return strings.TrimSuffix(w, "s")
	}
	return w
}

func unDouble(s string) string {
	if len(s) >= 3 && s[len(s)-1] == s[len(s)-2] {
		last := s[len(s)-1]
		if last == 'b' || last == 'd' || last == 'g' || last == 'm' || last == 'n' || last == 'p' || last == 'r' || last == 't' {
			return s[:len(s)-1]
		}
	}
	return s
}

// NewStandardAnalyzer creates the default production text analyzer.
func NewStandardAnalyzer() *Analyzer {
	return &Analyzer{
		Tokenizer: NewStandardTokenizer(),
		Filters: []TokenFilter{
			LowercaseFilter(),
			ASCIIFoldFilter(),
			StopWordsFilter(DefaultStopWords),
			StemmerFilter(),
		},
	}
}

// NewSimpleAnalyzer creates a lightweight whitespace + lowercase analyzer.
func NewSimpleAnalyzer() *Analyzer {
	return &Analyzer{
		Tokenizer: NewWhitespaceTokenizer(),
		Filters: []TokenFilter{
			LowercaseFilter(),
			ASCIIFoldFilter(),
		},
	}
}

// NewAutocompleteAnalyzer creates an analyzer producing edge n-grams for typeahead suggestions.
func NewAutocompleteAnalyzer(minGram, maxGram int) *Analyzer {
	return &Analyzer{
		Tokenizer: NewEdgeNGramTokenizer(minGram, maxGram),
		Filters: []TokenFilter{
			LowercaseFilter(),
			ASCIIFoldFilter(),
		},
	}
}
