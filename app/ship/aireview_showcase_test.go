//go:build showcase

package ship_test

import "testing"

func TestShowcaseAIReview(t *testing.T) {
	s := aiReviewScene(t)
	s.Shot("06-ai-review")
	s.Shot("p6-06-ai-review")
}
