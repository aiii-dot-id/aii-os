package agentmemory

import (
	"bytes"
	"fmt"
	"regexp"
	"regexp/syntax"
	"slices"
	"strings"
	"unicode"
)

var DefaultSecrets = []string{
	`(?i)-----BEGIN[ A-Z0-9_-]{0,100}PRIVATE KEY(?: BLOCK)?-----`,
	`PuTTY-User-Key-File-\d+:`,
	`AGE-SECRET-KEY-1[0-9A-Z]{58}`,
	`(?:^|[^A-Za-z0-9])sk-[A-Za-z0-9_-]{32,}`,
	`\bhf_[A-Za-z0-9]{34,}`,
	`\bapi_org_[A-Za-z0-9]{34,}`,
	`gh[pousr]_[0-9a-zA-Z]{36}`,
	`github_pat_\w{82}`,
	`glpat-[\w-]{20}`,
	`glptt-[0-9a-f]{40}`,
	`GR1348941[\w-]{20}`,
	`gl(?:rt|dt|ft|ffct|soat)-[0-9a-zA-Z_-]{20}`,
	`glcbt-[0-9a-zA-Z]{1,5}_[0-9a-zA-Z_-]{20}`,
	`glimt-[0-9a-zA-Z_-]{25}`,
	`glagent-[0-9a-zA-Z_-]{50}`,
	`gloas-[0-9a-zA-Z_-]{64}`,
	`_gitlab_session=[0-9a-z]{32}`,
	`\b(?:A3T[A-Z0-9]|AKIA|ASIA|ABIA|ACCA)[A-Z2-7]{16}\b`,
	awsSecretShape(),
	`\bABSK[A-Za-z0-9+/]{109,269}={0,2}`,
	`bedrock-api-key-YmVkcm9jay5hbWF6b25hd3MuY29t`,
	`xoxb-[0-9]{10,13}-[0-9]{10,13}[a-zA-Z0-9-]*`,
	`xox[pe](?:-[0-9]{10,13}){3}-[a-zA-Z0-9-]{28,34}`,
	`xox[abeoprs]-[A-Za-z0-9-]{10,}`,
	`(?i)\bxapp-\d-[A-Z0-9-]{10,}`,
	`(?:https?://)?hooks.slack.com/(?:services|workflows|triggers)/[A-Za-z0-9+/]{43,56}`,
	`\b(?:sk|rk)_(?:test|live|prod)_[a-zA-Z0-9]{10,99}`,
	`\bAIza[\w-]{35}`,
	`\bya29\.[0-9A-Za-z_-]{20,}`,
	`\bGOCSPX-[0-9A-Za-z_-]{28}`,
	`(?i)\bnpm_[a-z0-9]{36}`,
	`\b[0-9]{5,16}:A[0-9A-Za-z_-]{34}(?:[^0-9A-Za-z_-]|$)`,
	`\bey[a-zA-Z0-9]{17,}\.ey[a-zA-Z0-9/\\_-]{17,}\.`,
	jwtBase64,
	`AccountKey=[A-Za-z0-9+/]{80,}={0,2}`,
	`[a-zA-Z0-9_~.]{3}\dQ~[a-zA-Z0-9_~.-]{31,34}`,
	`\b[a-zA-Z][a-zA-Z0-9+.-]{1,20}://[^\s:/@'"]+:[^\s/@'"]+@[^\s/'"]+`,
	`\bA3-[A-Z0-9]{6}-(?:[A-Z0-9]{11}|[A-Z0-9]{6}-[A-Z0-9]{5})-[A-Z0-9]{5}-[A-Z0-9]{5}-[A-Z0-9]{5}\b`,
	`ops_eyJ[a-zA-Z0-9+/]{250,}={0,3}`,
	`\bp8e-[A-Za-z0-9]{32}`,
	`\bpat[[:alnum:]]{14}\.[a-f0-9]{64}\b`,
	`\bLTAI[A-Za-z0-9]{20}`,
	`\bAKCp[A-Za-z0-9]{69}\b`,
	`\bcmVmd[A-Za-z0-9]{59}\b`,
	`\b(?:sc|ext|scauth|authress)_[A-Za-z0-9]{5,30}\.[A-Za-z0-9]{4,6}\.acc[_-][A-Za-z0-9-]{10,32}\.[A-Za-z0-9+/_=-]{30,120}`,
	`\b4b1d[A-Za-z0-9]{38}\b`,
	`(?i)CLOJARS_[a-z0-9]{60}`,
	`\bv1\.0-[a-f0-9]{24}-[a-f0-9]{146}`,
	`\bdapi[a-f0-9]{32}`,
	`(?i)\bdo[opr]_v1_[a-f0-9]{64}`,
	`dp\.pt\.[A-Za-z0-9]{43}`,
	`duffel_(?:test|live)_[A-Za-z0-9_=-]{43}`,
	`dt0c01\.[A-Za-z0-9]{24}\.[A-Za-z0-9]{64}`,
	`\bEZ[AT]K[A-Za-z0-9]{54}\b`,
	`\bEAA[MC][A-Za-z0-9]{100,}`,
	`FLWSECK_TEST-[A-Ha-h0-9]{12}`,
	`FLWPUBK_TEST-[A-Ha-h0-9]{32}-X`,
	`\b(?:fo1_[\w-]{43}|fm1[ar]_[a-zA-Z0-9+/]{100,}={0,3}|fm2_[a-zA-Z0-9+/]{100,}={0,3})`,
	`fio-u-[A-Za-z0-9_=-]{64}`,
	`(?i)\beyJrIjoi[A-Za-z0-9]{70,400}={0,3}`,
	`(?i)\bglc_[A-Za-z0-9+/]{32,400}={0,3}`,
	`(?i)\bglsa_[A-Za-z0-9]{32}_[A-Fa-f0-9]{8}`,
	`(?:pat|sat)\.[a-zA-Z0-9_-]{22}\.[a-zA-Z0-9]{24}\.[a-zA-Z0-9]{20}`,
	`[A-Za-z0-9]{14}\.atlasv1\.[A-Za-z0-9_=-]{60,70}`,
	`\bHRKU-AA[0-9a-zA-Z_-]{58}`,
	`\bico-[a-zA-Z0-9]{32}`,
	`\bs-s4t2(?:ud|af)-[A-Fa-f0-9]{64}`,
	`lin_api_[A-Za-z0-9]{40}`,
	`\b[A-Za-z0-9]{6}_[A-Za-z0-9]{29}_mmk`,
	`https://[a-z0-9]+\.webhook\.office\.com/webhookb2/[a-z0-9]{8}-(?:[a-z0-9]{4}-){3}[a-z0-9]{12}@[a-z0-9]{8}-(?:[a-z0-9]{4}-){3}[a-z0-9]{12}/IncomingWebhook/[a-z0-9]{32}/[a-z0-9]{8}-(?:[a-z0-9]{4}-){3}[a-z0-9]{12}`,
	`\bntn_[0-9]{11}[A-Za-z0-9]{35}`,
	`\bAPI-[A-Z0-9]{26}`,
	`\bsha256~[\w-]{43}`,
	`\bpplx-[a-zA-Z0-9]{48}`,
	`(?i)\bpscale_(?:tkn|oauth|pw)_[\w=.-]{32,64}`,
	`\bPMAK-[A-Fa-f0-9]{24}-[A-Fa-f0-9]{34}`,
	`\bpnu_[a-zA-Z0-9]{36}`,
	`\bpul-[a-f0-9]{40}`,
	`pypi-AgEIcHlwaS5vcmc[\w-]{50,1000}`,
	`\brdme_[a-z0-9]{70}`,
	`\brubygems_[a-f0-9]{48}`,
	`\btk-us-[\w-]{48}`,
	`\bSG\.[A-Za-z0-9=_.-]{66}`,
	`\bxkeysib-[a-f0-9]{64}-[A-Za-z0-9]{16}`,
	`\bsntrys_eyJpYXQiO[a-zA-Z0-9+/]{10,200}(?:LCJyZWdpb25fdXJs|InJlZ2lvbl91cmwi|cmVnaW9uX3VybCI6)[a-zA-Z0-9+/]{10,200}={0,2}_[a-zA-Z0-9+/]{43}`,
	`\bsntryu_[a-f0-9]{64}`,
	`\bsm_[aps]at_[a-zA-Z0-9]{16}`,
	`\bshippo_(?:live|test)_[a-fA-F0-9]{40}`,
	`shp(?:at|ca|pa|ss)_[a-fA-F0-9]{32}`,
	`(?i)\bsgp_(?:(?:[a-f0-9]{16}|local)_)?[a-f0-9]{40}\b`,
	`\b(?:EAAA|sq0atp-)[\w-]{22,60}`,
	`\bsbp_(?:oauth_)?[a-f0-9]{40}`,
	`SK[0-9a-fA-F]{32}`,

	`(?i)\bNRAK-[a-z0-9]{27}`,
	`(?i)\bNRII-[a-z0-9-]{32}`,
	`(?i)\bNRJS-[a-f0-9]{19}`,
	`(?i)\bdnkey-[a-z0-9=_-]{26}-[a-z0-9=_-]{52}`,
	`(?i)\btfp_[a-z0-9._=-]{59}`,
	`\bAQVN[A-Za-z0-9_-]{35,38}`,
	`\bt1\.[A-Za-z0-9_-]+={0,2}\.[A-Za-z0-9_-]{86}`,
	`(?i)\bpubkey-[a-f0-9]{32}\b`,
	`(?i)\bsl\.[a-z0-9=_-]{135}`,
	`(?i)\baccess-(?:sandbox|development|production)-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`,
	`(?i)\bpk\.[a-z0-9]{60}\.[a-z0-9]{22}`,
	`\bA{22}[A-Za-z0-9%]{80,100}`,
	`(?i)\b(?:live|test)_[a-f0-9]{35}\b`,
	`(?i)\b(?:live|test)_pub_[a-f0-9]{31}\b`,
	`\bsq[upa]_[a-f0-9]{40}\b`,
	`(?i)\b[a-f0-9]{32}-us\d{2}\b`,
	`\bhvb\.[\w-]{138,300}`,
	`\b(?:hvs\.[\w-]{90,120}|s\.[A-Za-z0-9]{24}\b)`,

	`\bcurl\b(?:.*?|.*?(?:[\r\n]{1,2}.*?){1,5})[ \t\n\r](?:-H|--header)(?:=|[ \t]{0,5})(?:"(?i)(?:Authorization:[ \t]{0,5}(?:Basic[ \t](?:[a-z0-9+/]{8,}={0,3})|(?:Bearer|(?:Api-)?Token)[ \t](?:[\w=~@.+/-]{8,})|(?:[\w=~@.+/-]{8,}))|(?:(?:X-(?:[a-z]+-)?)?(?:Api-?)?(?:Key|Token)):[ \t]{0,5}(?:[\w=~@.+/-]{8,}))"|'(?i)(?:Authorization:[ \t]{0,5}(?:Basic[ \t](?:[a-z0-9+/]{8,}={0,3})|(?:Bearer|(?:Api-)?Token)[ \t](?:[\w=~@.+/-]{8,})|(?:[\w=~@.+/-]{8,}))|(?:(?:X-(?:[a-z]+-)?)?(?:Api-?)?(?:Key|Token)):[ \t]{0,5}(?:[\w=~@.+/-]{8,}))')(?:\B|\s|\z)`,
	`\bcurl\b(?:.*|.*(?:[\r\n]{1,2}.*){1,5})[ \t\n\r](?:-u|--user)(?:=|[ \t]{0,5})(?:"(?::[^"]{3,}|[^:"]{3,}:|[^:"]{3,}:[^"]{3,})"|'(?:[^:']{3,}:[^']{3,})'|(?:(?:"[^"]{3,}"|'[^']{3,}'|[\w$@.-]+):(?:"[^"]{3,}"|'[^']{3,}'|[\w${}@.-]+)))(?:\s|\z)`,
	`(?i)(?:\bkind:[ \t]*["']?\bsecret\b["']?(?s:.){0,200}?\bdata:(?s:.){0,100}?\s+(?:[\w.-]+:(?:[ \t]*(?:\||>[-+]?)\s+)?[ \t]*(?:["']?[a-z0-9+/]{10,}={0,3}["']?|\{\{[ \t\w"|$:=,.-]+}}|""|''))|\bdata:(?s:.){0,100}?\s+(?:[\w.-]+:(?:[ \t]*(?:\||>[-+]?)\s+)?[ \t]*(?:["']?[a-z0-9+/]{10,}={0,3}["']?|\{\{[ \t\w"|$:=,.-]+}}|""|''))(?s:.){0,200}?\bkind:[ \t]*["']?\bsecret\b["']?)`,
	`(?i)<add key=\"(?:(?:ClearText)?Password)\"\s*value=\"(?:.{8,})\"\s*/>`,
	`(?i)["']secret_key["']\s*=>\s*["'](?:sk_[\S]{29})["']`,
}

const jwtBase64 = `\bZXlK(?:aGJHY2lPaU|aGNIVWlPaU|aGNIWWlPaU|aGRXUWlPaU|aU5qUWlP|amNtbDBJanBi|amRIa2lPaU|bGNHc2lPbn|bGJtTWlPaU|cWEzVWlPaU|cWQyc2lPb|cGMzTWlPaU|cGRpSTZJ|cmFXUWlP|clpYbGZiM0J6SWpwY|cmRIa2lPaUp|dWIyNWpaU0k2|d01tTWlP|d01uTWlPaU|d2NIUWlPaU|emRXSWlPaU|emRuUWlP|MFlXY2lPaU|MGVYQWlPaUp|MWNtd2l|MWMyVWlPaUp|MlpYSWlPaU|MlpYSnphVzl1SWpv|NElqb2|NE5XTWlP|NE5YUWlPaU|NE5YUWpVekkxTmlJNkl|NE5YVWlPaU|NmFYQWlPaU)[a-zA-Z0-9/\\_+\-\r\n]{40,}={0,2}`

func awsSecretShape() string {
	ways := make([]string, 40)
	for i := range ways {
		ways[i] = fmt.Sprintf("[0-9a-f]{%d}[g-zA-Z/+][A-Za-z0-9/+]{%d}", i, 39-i)
	}
	return `(?i:aws).{0,20}(?i:secret).{0,20}[=:'"\s]{1,5}(` + strings.Join(ways, "|") + `)(?:[^A-Za-z0-9/+]|$)`
}

var Examples = []string{"AKIAIOSFODNN7EXAMPLE", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"}

type shape struct {
	re    *regexp.Regexp
	words [][]byte
}

func compileShapes(shapes []string) ([]shape, error) {
	res, err := CompileSecrets(shapes)
	if err != nil {
		return nil, err
	}
	out := make([]shape, len(res))
	for i, re := range res {
		out[i].re = re
		if tree, err := syntax.Parse(re.String(), syntax.Perl); err == nil {
			for _, w := range mustHold(tree) {
				out[i].words = append(out[i].words, []byte(w))
			}
		}
	}
	return out, nil
}

func mustHold(re *syntax.Regexp) []string {
	switch re.Op {
	case syntax.OpLiteral:
		return []string{string(fold([]byte(string(re.Rune))))}
	case syntax.OpCapture, syntax.OpPlus:
		return mustHold(re.Sub[0])
	case syntax.OpRepeat:
		if re.Min > 0 {
			return mustHold(re.Sub[0])
		}
	case syntax.OpConcat:

		var best []string
		for _, sub := range re.Sub {
			if w := mustHold(sub); w != nil && (best == nil || shortest(w) > shortest(best)) {
				best = w
			}
		}
		return best
	case syntax.OpAlternate:
		var all []string
		for _, sub := range re.Sub {
			w := mustHold(sub)
			if w == nil {
				return nil
			}
			all = append(all, w...)
		}
		return all
	}
	return nil
}

func shortest(words []string) int {
	n := len(words[0])
	for _, w := range words[1:] {
		n = min(n, len(w))
	}
	return n
}

func fold(text []byte) []byte {
	return bytes.Map(func(r rune) rune {
		if r == 'ſ' {
			return 's'
		}
		return unicode.ToLower(r)
	}, text)
}

func (c *copier) holdsSecret(data []byte) bool {
	folded := fold(data)
	for _, sh := range c.secrets {
		if sh.words != nil && !slices.ContainsFunc(sh.words, func(w []byte) bool { return bytes.Contains(folded, w) }) {
			continue
		}
		for _, m := range sh.re.FindAllSubmatch(data, -1) {
			value := m[0]
			if len(m) > 1 && m[1] != nil {
				value = m[1]
			}
			if !slices.Contains(Examples, string(value)) {
				return true
			}
		}
	}
	return false
}

func CompileSecrets(shapes []string) ([]*regexp.Regexp, error) {
	out := make([]*regexp.Regexp, 0, len(shapes))
	for _, s := range shapes {
		re, err := regexp.Compile(s)
		if err != nil {
			return nil, fmt.Errorf("%q is not a regular expression: %w", s, err)
		}
		out = append(out, re)
	}
	return out, nil
}
