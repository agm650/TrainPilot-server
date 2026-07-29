package service

import "github.com/agm650/TrainPilot-server/internal/model"

type Permission string

const (
	PermissionView      Permission = "view"
	PermissionDrive     Permission = "drive"
	PermissionDispatch  Permission = "dispatch"
	PermissionConfigure Permission = "configure"
)

func Allowed(role model.Role, p Permission) bool {
	switch role {
	case model.RoleViewer:
		return p == PermissionView
	case model.RoleDriver:
		return p == PermissionView || p == PermissionDrive
	case model.RoleDispatcher:
		return p == PermissionView || p == PermissionDrive || p == PermissionDispatch
	case model.RoleAdministrator:
		return true
	default:
		return false
	}
}
