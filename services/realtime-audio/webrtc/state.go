package webrtc

import realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"

func validConnectionStateTransition(from, to realtimev1.ConnectionState) bool {
	if from == to {
		return true
	}
	switch from {
	case realtimev1.ConnectionNew:
		return to == realtimev1.ConnectionConnecting ||
			to == realtimev1.ConnectionFailed || to == realtimev1.ConnectionClosed
	case realtimev1.ConnectionConnecting:
		return to == realtimev1.ConnectionConnected || to == realtimev1.ConnectionDisconnected ||
			to == realtimev1.ConnectionFailed || to == realtimev1.ConnectionClosed
	case realtimev1.ConnectionConnected:
		return to == realtimev1.ConnectionDisconnected ||
			to == realtimev1.ConnectionFailed || to == realtimev1.ConnectionClosed
	case realtimev1.ConnectionDisconnected:
		return to == realtimev1.ConnectionConnecting || to == realtimev1.ConnectionConnected ||
			to == realtimev1.ConnectionFailed || to == realtimev1.ConnectionClosed
	case realtimev1.ConnectionFailed:
		return to == realtimev1.ConnectionClosed
	default:
		return false
	}
}
