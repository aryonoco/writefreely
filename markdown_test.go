/*
 * Copyright © 2026 Musing Studio LLC.
 *
 * This file is part of WriteFreely.
 *
 * WriteFreely is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Affero General Public License, included
 * in the LICENSE file in this source code package.
 */

package writefreely

import (
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/writefreely/writefreely/config"
)

// markdownFixture returns a config for a blog at https://example.com and a
// helper that renders a post body for the blog at /blog/.
func markdownFixture() (cfg *config.Config, post func(string) string) {
	cfg = &config.Config{App: config.AppCfg{Host: "https://example.com"}}
	post = func(s string) string { return applyMarkdownSpecial([]byte(s), "/blog/", cfg, true) }
	return cfg, post
}

const markdownTestTable = "| a | b |\n|---|---|\n| 1 | 2 |"

// hashtagTests lists the inputs of TestHashtags and the tag names each one
// links.
var hashtagTests = []struct {
	name string
	in   string
	want []string
}{
	{"plain", "#tag", []string{"tag"}},
	{"mid sentence", "a #tag b", []string{"tag"}},
	{"mid word", "foo#bar", []string{"bar"}},
	{"digits only", "#123", nil},
	{"digits then letters", "#123abc", []string{"123abc"}},
	{"accented", "#über", []string{"über"}},
	{"CJK", "#日本語", []string{"日本語"}},
	{"apostrophe ends it", "#tag's", []string{"tag"}},
	{"language names", "C# and F#", nil},
	{"code span", "`#code`", nil},
	{"fenced code", "```\n#code\n```", nil},
	{"HTML block", "<div>\n#raw\n</div>", nil},
	{"autolink fragment", "https://example.com/#frag", nil},
	{"angle autolink fragment", "<https://example.com/#frag>", nil},
	{"URL fragment after a colon", "see:https://example.com/#frag", nil},
	{"URL fragment in brackets", "[https://example.com/#frag]", nil},
	{"URL fragment after a dash", "word—https://example.com/#frag", nil},
	{"URL fragment after a slash", "a/https://example.com/#frag", nil},
	{"URL fragment after a full-width colon", "详见：https://example.com/page#section", nil},
	{"URL fragment after CJK text", "参见https://example.com/page#section", nil},
	{"www URL fragment after a colon", "see:www.example.com/#frag", nil},
	{"www URL fragment in brackets", "[www.example.com/#frag]", nil},
	{"tag after a URL", "see https://example.com/ #tag", []string{"tag"}},
	{"tag after a link", "[see](https://example.com/) #tag", []string{"tag"}},
	{"tag after www in a word", "awww.#tag", []string{"tag"}},
	{"link text", "[see #tag](https://example.com)", nil},
	{"image alt", "![a #tag](/x.png)", nil},
	{"numeric entity", "&#39;s", nil},
	{"hex entity", "&#x41;", nil},
	{"heading", "## Heading #tag", []string{"tag"}},
	{"list item", "- item #tag", []string{"tag"}},
	{"blockquote", "> quote #tag", []string{"tag"}},
	{"table cell", "| a | b |\n|---|---|\n| #tag | x |", []string{"tag"}},
	{"emphasis", "*#tag*", []string{"tag"}},
	{"strikethrough", "~~#tag~~", []string{"tag"}},
	{"old marker text", "{{[[||x||]]}}", nil},
}

// hashtagTestAlso holds markup that a TestHashtags row's output must also
// contain.
var hashtagTestAlso = map[string]string{
	"link text": ">see #tag</a>",
	"image alt": `alt="a #tag"`,
}

func TestHashtags(t *testing.T) {
	t.Parallel()

	_, post := markdownFixture()
	for _, test := range hashtagTests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			out := post(test.in)
			for _, n := range test.want {
				// The sanitizer percent-encodes the name in the link's URL
				link := `href="/blog/tag:` + url.PathEscape(n) + `" class="hashtag"><span>#</span><span class="p-category">` + n + `</span></a>`
				if !strings.Contains(out, link) {
					t.Errorf("%q: no link for %q in %q", test.in, n, out)
				}
			}
			if got := strings.Count(out, `class="hashtag"`); got != len(test.want) {
				t.Errorf("%q: %d hashtag links, want %d, in %q", test.in, got, len(test.want), out)
			}
			if also, ok := hashtagTestAlso[test.name]; ok && !strings.Contains(out, also) {
				t.Errorf("%q: no %q in %q", test.in, also, out)
			}
		})
	}
}

func TestHashtagsNeedBaseURL(t *testing.T) {
	t.Parallel()

	cfg, _ := markdownFixture()
	if out := applyMarkdownSpecial([]byte("#tag"), "", cfg, true); strings.Contains(out, `class="hashtag"`) {
		t.Errorf("hashtag linked without a base URL: %q", out)
	}
}

func TestHashtagsChorus(t *testing.T) {
	t.Parallel()

	cfg, post := markdownFixture()
	cfg.App.Chorus = true
	want := `href="/read/t/tag" class="hashtag"`
	if out := post("#tag"); !strings.Contains(out, want) {
		t.Errorf("no %q in %q", want, out)
	}
}

func TestApplyMarkdownMentions(t *testing.T) {
	t.Parallel()

	_, post := markdownFixture()
	want := `<a href="https://example.com/@/alice@example.com" class="u-url mention">@<span>alice@example.com</span></a>`
	for _, in := range []string{
		"hi @alice@example.com",
		"@alice@example.com at the start",
		"(@alice@example.com)",
	} {
		t.Run(in, func(t *testing.T) {
			t.Parallel()

			if out := post(in); !strings.Contains(out, want) {
				t.Errorf("%q: no mention link in %q", in, out)
			}
		})
	}
}

func TestApplyMarkdownSpecialOutputs(t *testing.T) {
	t.Parallel()

	_, post := markdownFixture()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"table", markdownTestTable, "<table>"},
		{"fenced code", "```go\nx\n```", `<pre><code class="language-go">`},
		{"strikethrough", "~~x~~", "<del>x</del>"},
		{"heading ID", "## Hi there", `<h2 id="hi-there">Hi there</h2>`},
		{"apostrophe", "Don't", "Don’t"},
		{"dashes", "a -- b", "a – b"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if out := post(test.in); !strings.Contains(out, test.want) {
				t.Errorf("%q: no %q in %q", test.in, test.want, out)
			}
		})
	}

	t.Run("script", func(t *testing.T) {
		t.Parallel()

		if out := post("<script>alert(1)</script>x"); strings.Contains(out, "<script") {
			t.Errorf("script kept: %q", out)
		}
	})
}

// headingTagReg matches the opening tag of a heading.
var headingTagReg = regexp.MustCompile(`<h[1-6][^>]*>`)

func TestHeadingIDs(t *testing.T) {
	t.Parallel()

	_, post := markdownFixture()
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{
			"ATX levels",
			"# One\n\n## Two\n\n### Three\n\n#### Four\n\n##### Five\n\n###### Six\n\n## Closed ##\n\n####### seven",
			[]string{`<h1 id="one">`, `<h2 id="two">`, `<h3 id="three">`, `<h4 id="four">`, `<h5 id="five">`, `<h6 id="six">`, `<h2 id="closed">`},
		},
		{
			"duplicates",
			"## Same\n\ntext\n\n## Same\n\ntext\n\n## Same\n\n## Same!",
			[]string{`<h2 id="same">`, `<h2 id="same-1">`, `<h2 id="same-2">`, `<h2 id="same-3">`},
		},
		{
			"letters in any script",
			"## Привет мир\n\n## café déjà vu\n\n## 日本語の見出し\n\n## Emoji 🎉 heading\n\n## !!!",
			[]string{`<h2 id="привет-мир">`, `<h2 id="café-déjà-vu">`, `<h2 id="日本語の見出し">`, `<h2 id="emoji-heading">`, `<h2>`},
		},
		{"no letters", "## !!!", []string{`<h2>`}},
		{"two without letters", "## !!!\n\n## ???", []string{`<h2>`, `<h2>`}},
		{"hashtag", "## Heading #tag", []string{`<h2 id="heading-tag">`}},
		{"spaces", "## Hi there", []string{`<h2 id="hi-there">`}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			out := post(test.in)
			if got := headingTagReg.FindAllString(out, -1); !slices.Equal(got, test.want) {
				t.Errorf("%q: got %q, want %q", test.in, got, test.want)
			}
		})
	}
}

func TestApplyMarkdownKeepsHTMLBlocks(t *testing.T) {
	t.Parallel()

	_, post := markdownFixture()

	t.Run("split", func(t *testing.T) {
		t.Parallel()

		out := post("one\n\n<p class=\"split\">Split.</p>\n\ntwo")
		for _, want := range []string{"<p>one</p>", `<p class="split">Split.</p>`, "<p>two</p>"} {
			if !strings.Contains(out, want) {
				t.Errorf("no %q in %q", want, out)
			}
		}
	})

	// The emailsub short code between two lines of text: the form takes its
	// own block, and the Markdown after it still renders
	t.Run("emailsub form", func(t *testing.T) {
		t.Parallel()

		out := post(alterShortCodeEmailSubForm("Hello\n<!--emailsub-->\nBye *now*", "blog", "slug", false, ""))
		for _, want := range []string{
			"<p>Hello</p>",
			`<form method="post" id="emailsub" action="/api/collections/blog/email/subscribe"><input type="hidden" name="slug" value="slug"/>`,
			`<input type="email" name="email" placeholder="me@example.com"/><input type="submit" id="subscribe-btn" value="Subscribe"/></form>`,
			"<p>Bye <em>now</em></p>",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("no %q in %q", want, out)
			}
		}
	})

	t.Run("emailsub notice", func(t *testing.T) {
		t.Parallel()

		out := post(replaceShortCodeEmailSub("Hello\n<!--emailsub-->\nBye *now*", `<p id="emailsub">You're subscribed to email updates.</p>`))
		for _, want := range []string{
			"<p>Hello</p>",
			`<p id="emailsub">You&#39;re subscribed to email updates.</p>`,
			"<p>Bye <em>now</em></p>",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("no %q in %q", want, out)
			}
		}
	})

	t.Run("youtube", func(t *testing.T) {
		t.Parallel()

		out := post(`<iframe width="560" height="315" src="https://www.youtube.com/embed/abc?autoplay=1" frameborder="0" allowfullscreen></iframe>`)
		if !strings.Contains(out, "autoplay=0") {
			t.Errorf("autoplay not turned off: %q", out)
		}
	})
}

func TestApplyMarkdownTruncatedExcerpt(t *testing.T) {
	t.Parallel()

	cfg, _ := markdownFixture()
	out := applyMarkdownSpecial([]byte("intro\n\n```\ncode #tag"), "/blog/", cfg, true)
	if !strings.Contains(out, "<pre><code>") {
		t.Errorf("no code block in %q", out)
	}
	if strings.Contains(out, `class="hashtag"`) {
		t.Errorf("hashtag linked in code: %q", out)
	}
}

func TestApplyBasicMarkdownOddTitles(t *testing.T) {
	t.Parallel()

	for _, in := range []string{"a\nb", "#", "###", "<b>x</b> y", "---", "> q", "1. x"} {
		t.Run(in, func(t *testing.T) {
			t.Parallel()

			out := applyBasicMarkdown([]byte(in))
			if strings.Contains(out, "<h1") {
				t.Errorf("%q: heading markup in %q", in, out)
			}
		})
	}

	t.Run("raw HTML", func(t *testing.T) {
		t.Parallel()

		if out := applyBasicMarkdown([]byte("<b>x</b> y")); out != "x y" {
			t.Errorf("got %q, want %q", out, "x y")
		}
	})

	// A title that renders as more than one block loses its heading tags,
	// with nothing cut from its text
	for _, test := range []struct {
		name string
		in   string
		want string
	}{
		{"heading and paragraph", "a\nb", "a\nb"},
		{"two headings", "x\n# y", "x\ny"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if out := applyBasicMarkdown([]byte(test.in)); out != test.want {
				t.Errorf("%q: got %q, want %q", test.in, out, test.want)
			}
		})
	}
}

func TestApplyAPIMarkdown(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"heading attribute", "## Head {#custom}", `<h2 id="custom">Head</h2>`},
		{"table", markdownTestTable, "<table>"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if out := applyAPIMarkdown([]byte(test.in)); !strings.Contains(out, test.want) {
				t.Errorf("%q: no %q in %q", test.in, test.want, out)
			}
		})
	}

	t.Run("no hashtags", func(t *testing.T) {
		t.Parallel()

		if out := applyAPIMarkdown([]byte("#tag")); strings.Contains(out, `class="hashtag"`) {
			t.Errorf("hashtag linked: %q", out)
		}
	})
}

func TestApplyBasicAccessibleMarkdown(t *testing.T) {
	t.Parallel()

	t.Run("hard break", func(t *testing.T) {
		t.Parallel()

		want := "line one<br/>\nline two"
		if out := applyBasicAccessibleMarkdown([]byte("line one\nline two")); !strings.Contains(out, want) {
			t.Errorf("no %q in %q", want, out)
		}
	})

	t.Run("one line", func(t *testing.T) {
		t.Parallel()

		if out := applyBasicAccessibleMarkdown([]byte("one line")); out != "one line" {
			t.Errorf("got %q, want %q", out, "one line")
		}
	})

	t.Run("link", func(t *testing.T) {
		t.Parallel()

		out := applyBasicAccessibleMarkdown([]byte("[l](https://example.com)"))
		if !strings.Contains(out, `href="https://example.com"`) {
			t.Errorf("no link in %q", out)
		}
		if strings.Contains(out, "nofollow") {
			t.Errorf("nofollow added: %q", out)
		}
	})

	t.Run("strong", func(t *testing.T) {
		t.Parallel()

		if out := applyBasicAccessibleMarkdown([]byte("**b**")); !strings.Contains(out, "<strong>b</strong>") {
			t.Errorf("no strong text in %q", out)
		}
	})
}

func TestMarkdownEnginesConcurrent(t *testing.T) {
	t.Parallel()

	_, post := markdownFixture()
	apiInputs := []string{"## Head {#custom}", "#tag", markdownTestTable, "**b**", "```go\nx\n```"}

	wantPost := make([]string, len(hashtagTests))
	for i, test := range hashtagTests {
		wantPost[i] = post(test.in)
	}
	wantAPI := make([]string, len(apiInputs))
	for i, in := range apiInputs {
		wantAPI[i] = applyAPIMarkdown([]byte(in))
	}

	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for i, test := range hashtagTests {
				if got := post(test.in); got != wantPost[i] {
					t.Errorf("%q: got %q, want %q", test.in, got, wantPost[i])
				}
			}
			for i, in := range apiInputs {
				if got := applyAPIMarkdown([]byte(in)); got != wantAPI[i] {
					t.Errorf("%q: got %q, want %q", in, got, wantAPI[i])
				}
			}
		})
	}
	wg.Wait()
}
