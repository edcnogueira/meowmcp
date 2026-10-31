package domain

import "errors"

var (
	ErrNotLoggedIn      = errors.New("whatsapp is not logged in: start a login and have the user scan the QR code or enter the pairing code")
	ErrNotConnected     = errors.New("whatsapp is paired but not connected right now; check the status and retry shortly")
	ErrAlreadyLoggedIn  = errors.New("whatsapp is already logged in")
	ErrNotOnWhatsApp    = errors.New("phone number is not registered on WhatsApp")
	ErrInvalidRecipient = errors.New("invalid recipient: use a phone number with country code (e.g. +5511999998888) or a JID")
	ErrMessageNotFound  = errors.New("message not found in local history")
	ErrNoMedia          = errors.New("message has no downloadable media (text, location, contact, poll or similar)")
	ErrMediaMissingKey  = errors.New("local history has no download key for this attachment (old message synced without it); ask the sender to send it again")
	ErrMediaUnavailable = errors.New("media is no longer available on WhatsApp's servers; ask the sender to send it again")

	ErrApprovalRequired = errors.New("sending needs an approval token: the user's client mints it after the user approves this exact recipient and text, so ask the user to approve the message; do not retry on your own")
	ErrApprovalInvalid  = errors.New("approval token rejected; ask the user to approve the message again")
)
