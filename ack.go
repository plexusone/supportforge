package supportforge

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
	"unicode"

	supportcore "github.com/plexusone/supportforge-core"
)

// Template is the source of an acknowledgment, in Go's text/template
// syntax, with [AckData] as its data. Subject renders to one line; Body is
// Markdown.
type Template struct {
	Subject string
	Body    string
}

// TemplateKey selects a template. An empty Product or Kind matches any.
type TemplateKey struct {
	Product string
	Kind    supportcore.Kind
	Visitor bool
}

// AckData is what a template can use.
//
// Title and Body are set only for a signed-in requester. For a visitor they
// are empty whatever the template says, so an acknowledgment can never
// relay a visitor's text to the address they typed.
type AckData struct {
	Reference string
	Product   string
	Kind      supportcore.Kind
	KindName  string // "question", "report", "billing question", "feedback" or "message"
	Title     string
	Body      string
}

// Templates renders acknowledgments. Lookup goes from the most specific key
// to the defaults: product and kind, then product, then kind, then the
// default for signed-in requesters or visitors.
type Templates struct {
	byKey map[TemplateKey]*parsed
}

type parsed struct {
	subject, body *template.Template
}

var funcs = template.FuncMap{
	// quote prefixes each line with "> ", as a Markdown block quote.
	"quote": func(s string) string {
		if s == "" {
			return ""
		}
		return "> " + strings.ReplaceAll(s, "\n", "\n> ")
	},
}

const (
	defaultSubject = "We received your {{.KindName}} ({{.Reference}})"
	signedInBody   = `Thank you for getting in touch. We have received your {{.KindName}} and will reply by email.

Reference: {{.Reference}}

**{{.Title}}**

{{quote .Body}}
`
	visitorBody = `Thank you for getting in touch. We have received your {{.KindName}} and will reply by email.

Reference: {{.Reference}}
`
)

// DefaultTemplates returns the default acknowledgments.
func DefaultTemplates() *Templates {
	t, err := NewTemplates(nil)
	if err != nil {
		panic(err) // the defaults are constants; a parse error is a bug
	}
	return t
}

// NewTemplates returns the defaults overridden by the given templates.
func NewTemplates(overrides map[TemplateKey]Template) (*Templates, error) {
	all := map[TemplateKey]Template{
		{Visitor: false}: {Subject: defaultSubject, Body: signedInBody},
		{Visitor: true}:  {Subject: defaultSubject, Body: visitorBody},
	}
	for k, v := range overrides {
		all[k] = v
	}
	t := &Templates{byKey: map[TemplateKey]*parsed{}}
	for k, v := range all {
		subject, err := template.New("subject").Funcs(funcs).Option("missingkey=error").Parse(v.Subject)
		if err != nil {
			return nil, fmt.Errorf("supportforge: template %+v subject: %w", k, err)
		}
		body, err := template.New("body").Funcs(funcs).Option("missingkey=error").Parse(v.Body)
		if err != nil {
			return nil, fmt.Errorf("supportforge: template %+v body: %w", k, err)
		}
		t.byKey[k] = &parsed{subject, body}
	}
	return t, nil
}

// Render returns the acknowledgment for c.
func (t *Templates) Render(c supportcore.Case) (supportcore.Acknowledgment, error) {
	visitor := c.Requester.Visitor()
	data := AckData{Reference: c.Reference, Product: c.Product, Kind: c.Kind, KindName: kindName(c.Kind)}
	if !visitor {
		data.Title, data.Body = c.Title, c.Body
	}
	p := t.lookup(c.Product, c.Kind, visitor)
	var subject, body bytes.Buffer
	if err := p.subject.Execute(&subject, data); err != nil {
		return supportcore.Acknowledgment{}, fmt.Errorf("supportforge: rendering the subject: %w", err)
	}
	if err := p.body.Execute(&body, data); err != nil {
		return supportcore.Acknowledgment{}, fmt.Errorf("supportforge: rendering the body: %w", err)
	}
	line := strings.Join(strings.FieldsFunc(subject.String(), unicode.IsControl), " ")
	return supportcore.Acknowledgment{Subject: strings.TrimSpace(line), Body: body.String()}, nil
}

func (t *Templates) lookup(product string, kind supportcore.Kind, visitor bool) *parsed {
	for _, k := range []TemplateKey{
		{product, kind, visitor},
		{product, "", visitor},
		{"", kind, visitor},
		{"", "", visitor},
	} {
		if p, ok := t.byKey[k]; ok {
			return p
		}
	}
	panic("supportforge: no default template") // NewTemplates always adds both defaults
}

func kindName(k supportcore.Kind) string {
	switch k {
	case supportcore.KindQuestion:
		return "question"
	case supportcore.KindProblem:
		return "report"
	case supportcore.KindBilling:
		return "billing question"
	case supportcore.KindFeedback:
		return "feedback"
	}
	return "message"
}
