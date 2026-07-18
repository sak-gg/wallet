package handler

import "github.com/gin-gonic/gin"

func NewRouter(h *Handler) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())

	r.POST("/wallets", h.CreateWallet)
	r.POST("/wallets/:id/topup", h.TopUp)
	r.POST("/wallets/:id/deduct", h.Deduct)
	r.GET("/wallets/:id/balance", h.GetBalance)
	r.GET("/wallets/:id/transactions", h.ListTransactions)

	return r
}
