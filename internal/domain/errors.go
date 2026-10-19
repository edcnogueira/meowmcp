package domain

import "errors"

var (
	ErrNotLoggedIn      = errors.New("whatsapp is not logged in: start a login and have the user scan the QR code or enter the pairing code")
	ErrNotConnected     = errors.New("whatsapp is paired but not connected right now; check the status and retry shortly")
	ErrAlreadyLoggedIn  = errors.New("whatsapp is already logged in")
	ErrNotOnWhatsApp    = errors.New("phone number is not registered on WhatsApp")
	ErrInvalidRecipient = errors.New("invalid recipient: use a phone number with country code (e.g. +5511999998888) or a JID")
	ErrMessageNotFound  = errors.New("message not found in local history")
)
