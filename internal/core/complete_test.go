package core

import (
	"strings"
	"testing"
)

func TestCompletionContextAt(t *testing.T) {
	isModule := func(id string) bool { return id == "styles" }
	tests := []struct {
		src  string // | marks the cursor
		want string // kind:prefix:word[:module][:siblings]
	}{
		{`<div className="card card__ti|tle x"/>`, "class:card__ti:card__title:::card x"},
		{`<div className="|"/>`, "class:::"},
		{`<div className="a |"/>`, "class:::::a"},
		{`<div className={clsx('a', on && 'a--|')}/>`, "class:a--:a--:"},
		{"<div className={`a ${on ? 'b|' : ''}`}/>", "class:b:b:"},
		{"<div className={`a b|`}/>", "class:b:b:::a"},
		{`<div title="x|"/>`, "none"},
		{`<div className={styles.|}/>`, "member:::styles"},
		{`<div className={styles.ro|ot}/>`, "member:ro:root:styles"},
		{`<div className={styles['my-|']}/>`, "member:my-:my-:styles:bracket"},
		{`<div className={other.ro|ot}/>`, "none"},
		{`const x = 'a|'`, "none"},
		{`<div className={t('key|')} title={'x|'}/>`, "class:key:key:"}, // any string in a class expression
		{`<div className="unterminated |`, "class:::::unterminated"},
	}
	for _, tt := range tests {
		off := strings.Index(tt.src, "|")
		src := tt.src[:off] + tt.src[off+1:]
		c := CompletionContextAt(src, off, nil, isModule)
		var got string
		switch c.Kind {
		case CompleteNone:
			got = "none"
		case CompleteClass:
			got = "class:" + c.Prefix + ":" + src[c.Start:c.End] + ":"
			if len(c.Siblings) > 0 {
				got += "::" + strings.Join(c.Siblings, " ")
			}
		case CompleteModuleMember:
			got = "member:" + c.Prefix + ":" + src[c.Start:c.End] + ":" + c.Module
			if c.Bracket {
				got += ":bracket"
			}
		}
		if got != tt.want {
			t.Errorf("%s\n got %q\nwant %q", tt.src, got, tt.want)
		}
	}
}
