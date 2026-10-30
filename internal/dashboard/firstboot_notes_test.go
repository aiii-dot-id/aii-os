package dashboard

import (
	"regexp"
	"strings"
	"testing"
)

func TestTheBirthFormSaysWhatABirthCopies(t *testing.T) {
	frame, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(frame)
	form := html[strings.Index(html, `id="firstboot"`):strings.Index(html, `id="fb-result"`)]
	said := regexp.MustCompile(`<p class="fb-meet" data-fb-notes>([^<]*)</p>`).FindStringSubmatch(form)
	if said == nil {
		t.Fatal("the birth form does not say that other agents' notes are copied, above its result line")
	}
	if strings.Index(form, "data-fb-notes") > strings.Index(form, `id="fb-birth"`) {
		t.Fatal("the form says it below the Birth button")
	}
	for _, want := range []string{"other AI agents kept on this machine", "by default Claude Code, the Claude desktop app and Codex", "the stores set in genesis.agent_memory", "copied into your identity&rsquo;s home at birth", "you are told what was copied", "Nothing else in those folders is copied", "looks like a key or a token is left out", "genesis.agent_memory.off"} {
		if !strings.Contains(said[1], want) {
			t.Errorf("the form does not say %q: %s", want, said[1])
		}
	}
}
