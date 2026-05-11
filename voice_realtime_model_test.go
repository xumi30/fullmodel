package fullmodel

import "testing"

func TestRealtimeTTSModelResolved_VCAndBuiltinVoiceUsesFlash(t *testing.T) {
	got := realtimeTTSModelResolved(QwenTTSVCRealtimeModel, "Cherry")
	if got != QwenTTSFlashRealtimeModel {
		t.Fatalf("expected %q, got %q", QwenTTSFlashRealtimeModel, got)
	}
}

func TestRealtimeTTSModelResolved_VCAndCloneIDStaysVC(t *testing.T) {
	const clone = "fullmodel_vc_7a2f9c"
	got := realtimeTTSModelResolved(QwenTTSVCRealtimeModel, clone)
	if got != QwenTTSVCRealtimeModel {
		t.Fatalf("expected unchanged VC model, got %q", got)
	}
}

func TestRealtimeTTSModelResolved_FlashUnchanged(t *testing.T) {
	got := realtimeTTSModelResolved(QwenTTSFlashRealtimeModel, "Cherry")
	if got != QwenTTSFlashRealtimeModel {
		t.Fatalf("expected %q, got %q", QwenTTSFlashRealtimeModel, got)
	}
}
