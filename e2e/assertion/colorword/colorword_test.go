package colorword

import "testing"

func TestMatchesLiteralColorWord(t *testing.T) {
	for _, value := range []string{"Red", "red", "RED", "Red.", "red!", "RED?", `"Red"`, `'red.'`, `"RED!"`, `"Red".`, " \tRed.\n"} {
		t.Run(value, func(t *testing.T) {
			if !Matches(value, "red") {
				t.Fatal("a single requested color word was rejected")
			}
		})
	}
}

func TestMatchesRejectsDifferentMeaningOrExtraWords(t *testing.T) {
	for _, value := range []string{"blue", "green", "red and blue", "mostly red", "I cannot tell", "", "red!foo", "red..", `"red" blue`, `"red'`, "red\nblue", "réd", "<red>", "`red`", `" red "`, `"Red.".`, `"red!"?`} {
		t.Run(value, func(t *testing.T) {
			if Matches(value, "red") {
				t.Fatal("a different color, narration, refusal or malformed readout was accepted")
			}
		})
	}
}

func TestMatchesValidatesExpectedWord(t *testing.T) {
	if !Matches("BLUE.", "blue") {
		t.Fatal("the validator must support the requested color rather than hardcode red")
	}
	for _, expected := range []string{"", "red or blue", "red.*", "red.", "réd"} {
		if Matches("red", expected) {
			t.Fatal("invalid expected color accepted")
		}
	}
}
