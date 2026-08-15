package tokens

// Class is the content-type classification Classify assigns and Estimate prices by. Each class
// carries its own characters-per-token (or pixels/pages-per-token) constant in
// config.RuntimeCfg.Tokens, so pricing a class never means editing this package (D11, §11.6).
type Class uint8

// The seven classes, in the order 00-ARCHITECTURE.md §5.20 lists them.
const (
	ClassProse Class = iota
	ClassCode
	ClassJSON
	ClassDiff
	ClassImage
	ClassPDF
	ClassBinary
)

// String renders the class the way log lines and test failure messages should show it.
func (c Class) String() string {
	switch c {
	case ClassProse:
		return "prose"
	case ClassCode:
		return "code"
	case ClassJSON:
		return "json"
	case ClassDiff:
		return "diff"
	case ClassImage:
		return "image"
	case ClassPDF:
		return "pdf"
	case ClassBinary:
		return "binary"
	default:
		return "unknown"
	}
}
