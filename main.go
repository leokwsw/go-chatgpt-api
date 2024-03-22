package main

import (
	"flag"
	"fmt"
	"github.com/denisbrodbeck/machineid"
	"github.com/gin-gonic/gin"
	"github.com/leokwsw/go-chatgpt-api/api"
	"github.com/leokwsw/go-chatgpt-api/api/chatgpt"
	"github.com/leokwsw/go-chatgpt-api/api/imitate"
	"github.com/leokwsw/go-chatgpt-api/api/platform"
	_ "github.com/leokwsw/go-chatgpt-api/env"
	"github.com/leokwsw/go-chatgpt-api/middleware"
	"github.com/linweiyuan/go-logger/logger"
	"log"
	"strings"

	http "github.com/bogdanfinn/fhttp"
)

func init() {
	gin.ForceConsoleColor()
	gin.SetMode(gin.ReleaseMode)
}

func main() {
	id, err := machineid.ProtectedID("go-chatgpt-api")
	if err != nil {
		log.Fatal(err)
	}
	logger.Info("id : " + id)

	flatPort := flag.String("port", "8080", "")
	flag.Parse()

	router := gin.Default()
	router.Use(middleware.CORS())
	router.Use(middleware.Authorization())

	setupChatGPTAPIs(router)
	setupPlatformAPIs(router)
	setupPandoraAPIs(router)
	setupImitateAPIs(router)

	router.NoRoute(api.Proxy)

	router.GET("/", func(c *gin.Context) {
		c.Header("Content-Type", "text/plain")
		c.String(http.StatusOK, api.ReadyHint)
	})

	router.GET("/robots.txt", func(c *gin.Context) {
		c.Header("Content-Type", "text/plain")
		c.String(http.StatusOK, api.RobotsHint)
	})

	port := *flatPort

	rErr := router.Run(":" + port)
	if rErr != nil {
		log.Fatal("Failed to start Http server: " + rErr.Error())
	} else {
		fmt.Println("run on port :" + port)
	}
}

func setupChatGPTAPIs(router *gin.Engine) {
	chatgptGroup := router.Group("/chatgpt")
	{
		chatgptGroup.POST("/login", chatgpt.Login)
		chatgptGroup.POST("/backend-api/login", chatgpt.Login) // add support for other projects

		conversationsGroup := chatgptGroup.Group("/conversations")
		{
			conversationsGroup.GET("", chatgpt.GetConversations)

			// PATCH is official method, POST is added for Java support
			conversationsGroup.PATCH("", chatgpt.ClearConversations)
			conversationsGroup.POST("", chatgpt.ClearConversations)
		}

		backendConversationGroup := chatgptGroup.Group("/backend-api/conversation")
		{
			backendConversationGroup.POST("", chatgpt.CreateConversation)
		}

		conversationGroup := chatgptGroup.Group("/conversation")
		{
			conversationGroup.POST("", chatgpt.CreateConversation)
			conversationGroup.POST("/gen_title/:id", chatgpt.GenerateTitle)
			conversationGroup.GET("/:id", chatgpt.GetConversation)

			// rename or delete conversation use a same API with different parameters
			conversationGroup.PATCH("/:id", chatgpt.UpdateConversation)
			conversationGroup.POST("/:id", chatgpt.UpdateConversation)

			conversationGroup.POST("/message_feedback", chatgpt.FeedbackMessage)
		}

		// misc
		chatgptGroup.GET("/models", chatgpt.GetModels)
		chatgptGroup.GET("/accounts/check", chatgpt.GetAccountCheck)

		chatgptGroup.GET("/ping", chatgpt.Ping)
	}
}

func setupPlatformAPIs(router *gin.Engine) {
	platformGroup := router.Group("/platform")
	{
		platformGroup.POST("/login", platform.Login)

		apiGroup := platformGroup.Group("/v1")
		{
			apiGroup.POST("/login", platform.Login)
			apiGroup.GET("/models", platform.ListModels)
			apiGroup.GET("/models/:model", platform.RetrieveModel)
			apiGroup.POST("/completions", platform.CreateCompletions)
			apiGroup.POST("/chat/completions", platform.CreateChatCompletions)
			apiGroup.POST("/edits", platform.CreateEdit)
			apiGroup.POST("/images/generations", platform.CreateImage)
			apiGroup.POST("/embeddings", platform.CreateEmbeddings)
			apiGroup.GET("/files", platform.ListFiles)
			apiGroup.POST("/moderations", platform.CreateModeration)
		}

		dashboardGroup := platformGroup.Group("/dashboard")
		{
			billingGroup := dashboardGroup.Group("/billing")
			{
				billingGroup.GET("/credit_grants", platform.GetCreditGrants)
				billingGroup.GET("/subscription", platform.GetSubscription)
			}

			userGroup := dashboardGroup.Group("/user")
			{
				userGroup.GET("/api_keys", platform.GetApiKeys)
			}
		}
	}
}

func setupPandoraAPIs(router *gin.Engine) {
	router.Any("/api/*path", func(c *gin.Context) {
		c.Request.URL.Path = strings.ReplaceAll(c.Request.URL.Path, "/api", "/chatgpt/backend-api")
		router.HandleContext(c)
	})
}

func setupImitateAPIs(router *gin.Engine) {
	imitateGroup := router.Group("/imitate")
	{
		imitateGroup.POST("/login", chatgpt.Login)

		apiGroup := imitateGroup.Group("/v1")
		{
			apiGroup.POST("/chat/completions", imitate.CreateChatCompletions)
		}
	}
}
