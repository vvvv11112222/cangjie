package academic

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/vvvv11112222/cangjie/internal/identity"
)

func TestPatchOfferingRejectsMalformedUUIDsBeforeComparison(t *testing.T) {
	valid := "0e4ec10a-8462-4d0f-94ce-75ecf25f1852"
	fields := []struct {
		name  string
		patch func(*string) PatchOffering
	}{
		{"org_unit_id", func(value *string) PatchOffering { return PatchOffering{OrgUnitID: value} }},
		{"term_id", func(value *string) PatchOffering { return PatchOffering{TermID: value} }},
		{"course_id", func(value *string) PatchOffering { return PatchOffering{CourseID: value} }},
		{"teacher_id", func(value *string) PatchOffering { return PatchOffering{TeacherID: value} }},
		{"class_group_id", func(value *string) PatchOffering { return PatchOffering{ClassGroupID: value} }},
	}

	for _, field := range fields {
		t.Run(field.name, func(t *testing.T) {
			malformed := strings.Replace(valid, "-", "!", 1)
			_, err := (&Service{}).PatchOffering(context.Background(), identity.Principal{}, valid, field.patch(&malformed))
			assertAppError(t, err, http.StatusBadRequest, "INVALID_ARGUMENT")
		})
	}
}
