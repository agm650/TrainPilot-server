package service

import (
	"testing"

	"github.com/agm650/TrainPilot-server/internal/model"
)

func TestPermissionMatrix(t *testing.T) {
	tests := []struct {
		role       model.Role
		permission Permission
		allowed    bool
	}{
		{model.RoleViewer, PermissionView, true},
		{model.RoleViewer, PermissionDrive, false},
		{model.RoleDriver, PermissionDrive, true},
		{model.RoleDriver, PermissionDispatch, false},
		{model.RoleDispatcher, PermissionDispatch, true},
		{model.RoleAdministrator, PermissionConfigure, true},
	}
	for _, tc := range tests {
		if got := Allowed(tc.role, tc.permission); got != tc.allowed {
			t.Fatalf("role=%s permission=%s got=%v want=%v", tc.role, tc.permission, got, tc.allowed)
		}
	}
}
