package report

import "testing"

func TestAnalysisReportPath(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"flac with dir", "/x/LMP-81-mark.flac", "/x/LMP-81-mark-flac-analysis.md"},
		{"wav folds extension", "/a/b/voice.wav", "/a/b/voice-wav-analysis.md"},
		{"no extension", "/tmp/raw", "/tmp/raw-analysis.md"},
		{"basename only", "sample.aiff", "sample-aiff-analysis.md"},
		{"dotted name keeps stem", "/d/take.01.flac", "/d/take.01-flac-analysis.md"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := AnalysisReportPath(tc.in); got != tc.want {
				t.Fatalf("AnalysisReportPath(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestAnalysisReportPath_CollidingStemsDistinct asserts inputs that share a stem
// but differ by extension in the same directory map to distinct report paths, so
// a mixed-format batch (foo.flac and foo.wav) never clobbers one report with
// another.
func TestAnalysisReportPath_CollidingStemsDistinct(t *testing.T) {
	flac := AnalysisReportPath("/batch/foo.flac")
	wav := AnalysisReportPath("/batch/foo.wav")
	if flac == wav {
		t.Fatalf("colliding stems produced the same report path: %q", flac)
	}
	if flac != "/batch/foo-flac-analysis.md" {
		t.Errorf("foo.flac report = %q, want /batch/foo-flac-analysis.md", flac)
	}
	if wav != "/batch/foo-wav-analysis.md" {
		t.Errorf("foo.wav report = %q, want /batch/foo-wav-analysis.md", wav)
	}
}
