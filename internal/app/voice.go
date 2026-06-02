package app

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"

	"mafia-server/internal/httpx"
	"mafia-server/internal/rooms"
)

type voiceTokenResponse struct {
	Token string `json:"token"`
	URL   string `json:"url"`
}

// voiceToken issues a short-lived LiveKit access token so a room participant can
// join the matching LiveKit voice/video room. The LiveKit room name is the game
// room ID, so all players in a game share one media room.
//
// @Summary Get a LiveKit voice/video token
// @Description Mint a LiveKit access token for the authenticated room participant
// @Security Bearer
// @Produce json
// @Param roomId path string true "Room ID"
// @Success 200 {object} voiceTokenResponse
// @Failure 403 {object} map[string]string "Not a participant"
// @Failure 404 {object} map[string]string "Room not found"
// @Failure 503 {object} map[string]string "Voice not configured"
// @Router /api/games/{roomId}/voice-token [post]
func (h *Handler) voiceToken(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}

	if h.liveKitAPIKey == "" || h.liveKitAPISecret == "" || h.liveKitURL == "" {
		httpx.WriteError(w, http.StatusServiceUnavailable, "Голосовий чат не налаштований на сервері.")
		return
	}

	roomID := r.PathValue("roomId")
	room, ok := h.rooms.GetRoom(roomID)
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, rooms.ErrRoomNotFound.Error())
		return
	}
	if !isRoomParticipant(room, user.ID) {
		httpx.WriteError(w, http.StatusForbidden, rooms.ErrNotParticipant.Error())
		return
	}

	token, err := mintLiveKitToken(h.liveKitAPIKey, h.liveKitAPISecret, roomID, user.ID, user.Nickname)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Не вдалося видати голосовий токен.")
		return
	}

	h.presence.touch(roomID)
	httpx.WriteJSON(w, http.StatusOK, voiceTokenResponse{Token: token, URL: h.liveKitURL})
}

// mintLiveKitToken builds a LiveKit access token. A LiveKit token is a standard
// HS256 JWT signed with the API secret, carrying the VideoGrant under the
// "video" claim — so we craft it with the stdlib and avoid the heavy LiveKit
// server SDK (which pulls in the whole WebRTC stack just to sign a JWT).
func mintLiveKitToken(apiKey, apiSecret, room, identity, name string) (string, error) {
	now := time.Now()
	header := map[string]string{"alg": "HS256", "typ": "JWT"}
	claims := map[string]any{
		"iss":  apiKey,
		"sub":  identity,
		"nbf":  now.Unix(),
		"exp":  now.Add(2 * time.Hour).Unix(),
		"name": name,
		"video": map[string]any{
			"room":           room,
			"roomJoin":       true,
			"canPublish":     true,
			"canSubscribe":   true,
			"canPublishData": true,
		},
	}

	encode := func(v any) (string, error) {
		raw, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		return base64.RawURLEncoding.EncodeToString(raw), nil
	}

	headerPart, err := encode(header)
	if err != nil {
		return "", err
	}
	claimsPart, err := encode(claims)
	if err != nil {
		return "", err
	}

	signingInput := headerPart + "." + claimsPart
	mac := hmac.New(sha256.New, []byte(apiSecret))
	mac.Write([]byte(signingInput))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	return signingInput + "." + signature, nil
}
