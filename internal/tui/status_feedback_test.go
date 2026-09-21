package tui

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/julienhmmt/dockerdownloader/pkg/imagelist"
)

func TestViewReview_ShowsStatusLine(t *testing.T) {
	m := newTestModel()
	m.state = stateReview
	m.setStatus("Select at least one image (space), or press a to add one.")
	out := m.render()
	assert.Contains(t, out, "Select at least one image")
}

func TestUpdateReview_EnterZeroSelectedSetsStatus(t *testing.T) {
	m := newTestModel()
	m.state = stateReview
	m.reviewImages = []imagelist.Image{{Ref: "nginx:1.27", Selected: false}}
	got, _ := m.handleReviewKey(keyPress("enter"))
	m2 := got.(model)
	assert.Equal(t, stateReview, m2.state)
	assert.Contains(t, m2.status, "Select at least one image")
}

func TestHandleAddImageKey_InvalidRefSetsStatus(t *testing.T) {
	tests := []struct {
		name string
		ref  string
	}{
		{name: "spaces", ref: "not a ref"},
		{name: "unparseable", ref: "!!!invalid:ref"},
		{name: "template", ref: "image:{{ .Values.tag }}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestModel()
			m.state = stateAddImage
			before := len(m.reviewImages)
			m.addInput.SetValue(tt.ref)
			got, _ := m.handleAddImageKey(keyPress("enter"))
			m2 := got.(model)
			assert.Equal(t, stateAddImage, m2.state)
			assert.Contains(t, m2.status, "Invalid image reference")
			assert.Len(t, m2.reviewImages, before, "invalid ref must not be appended")
		})
	}
}

func TestHandleAddImageKey_ValidRefAppendsAndClearsStatus(t *testing.T) {
	m := newTestModel()
	m.state = stateAddImage
	m.addInput.SetValue("nginx:1.27")
	got, _ := m.handleAddImageKey(keyPress("enter"))
	m2 := got.(model)
	assert.Equal(t, stateReview, m2.state)
	assert.Empty(t, m2.status)
	require.Len(t, m2.reviewImages, 3)
	assert.Equal(t, "nginx:1.27", m2.reviewImages[2].Ref)
	assert.True(t, m2.reviewImages[2].Selected)
}

func TestHandleReviewKey_EscQuits(t *testing.T) {
	m := newTestModel()
	m.state = stateReview
	_, cmd := m.handleReviewKey(keyPress("esc"))
	assert.NotNil(t, cmd)
}

func TestHandleEndKey_QuitsOnQ(t *testing.T) {
	m := newTestModel()
	m.state = stateDone
	_, cmd := m.handleEndKey(keyPress("q"))
	assert.NotNil(t, cmd)
}

func TestHandleEndKey_IgnoresUnknownKeys(t *testing.T) {
	m := newTestModel()
	m.state = stateDone
	got, cmd := m.handleEndKey(keyPress("z"))
	assert.Equal(t, stateDone, got.(model).state)
	assert.Nil(t, cmd)
}
