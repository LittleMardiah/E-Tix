package queue

import "time"

const (
	TierVIP     = "TIER_1"
	TierRegular = "TIER_2"
	TierLow     = "TIER_3"
)

type QueueStatusResponse struct {
	Position             int    `json:"position"`
	TotalInQueue         int    `json:"total_in_queue"`
	EstimatedWaitSeconds int    `json:"estimated_wait_seconds"`
	Tier                 string `json:"tier"`
	TierLabel            string `json:"tier_label"`
	LoyaltyScore         int    `json:"loyalty_score"`
	QueueToken           string `json:"queue_token"`
}

type FanScoreMetadata struct {
	UserID     string    `json:"user_id"`
	IP         string    `json:"ip"`
	FinalScore int       `json:"final_score"`
	Timestamp  time.Time `json:"timestamp"`
}
