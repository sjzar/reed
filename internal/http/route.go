package http

import (
	"net/http"
	"os"

	"github.com/gin-gonic/gin"

	"github.com/sjzar/reed/internal/errors"
)

func (s *Service) initRouter() {
	s.router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	v1 := s.router.Group("/v1")
	v1.GET("/ping", s.handlePing)
	v1.GET("/status", s.handleStatus)
	v1.GET("/runs/:runID", s.handleGetRun)
	v1.POST("/runs/:runID/stop", s.handleStopRun)
	v1.GET("/media/:id", s.handleGetMedia)

	api := s.router.Group("/api/v1")
	api.Use(nocacheMiddleware())
	api.GET("/ping", s.handlePing)
	api.GET("/status", s.handleStatus)
	api.GET("/runs/:runID", s.handleGetRun)
	api.POST("/runs/:runID/stop", s.handleStopRun)
	api.GET("/media/:id", s.handleGetMedia)
}

func (s *Service) handlePing(c *gin.Context) {
	if s.provider == nil {
		s.RespondError(c, errors.New(errors.CodeUnavailable, "no status provider"))
		return
	}
	s.Respond(c, s.provider.PingData())
}

func (s *Service) handleStatus(c *gin.Context) {
	if s.provider == nil {
		s.RespondError(c, errors.New(errors.CodeUnavailable, "no status provider"))
		return
	}
	data, err := s.provider.StatusData()
	if err != nil {
		s.RespondError(c, errors.Wrap(err, errors.CodeUnavailable, "status unavailable"))
		return
	}
	s.Respond(c, data)
}

func (s *Service) handleGetRun(c *gin.Context) {
	if s.provider == nil {
		s.RespondError(c, errors.New(errors.CodeUnavailable, "no status provider"))
		return
	}
	runID := c.Param("runID")
	data, ok := s.provider.RunData(runID)
	if !ok {
		s.RespondError(c, errors.NewNotFound("run not found: "+runID))
		return
	}
	s.Respond(c, data)
}

func (s *Service) handleStopRun(c *gin.Context) {
	if s.provider == nil {
		s.RespondError(c, errors.New(errors.CodeUnavailable, "no status provider"))
		return
	}
	runID := c.Param("runID")
	if !s.provider.StopRun(runID) {
		s.RespondError(c, errors.NewNotFound("run not found or already terminal: "+runID))
		return
	}
	s.Respond(c, gin.H{"runID": runID, "stopped": true})
}

func (s *Service) handleGetMedia(c *gin.Context) {
	if s.media == nil {
		s.RespondError(c, errors.New(errors.CodeUnavailable, "media service not available"))
		return
	}
	id := c.Param("id")
	rc, entry, err := s.media.Open(c.Request.Context(), id)
	if err != nil {
		if os.IsNotExist(err) {
			s.RespondError(c, errors.NewNotFound("media not found: "+id))
		} else {
			s.RespondError(c, errors.Wrap(err, errors.CodeInternal, "media read error"))
		}
		return
	}
	defer rc.Close()
	c.DataFromReader(http.StatusOK, entry.Size, entry.MIMEType, rc, map[string]string{
		"Cache-Control": "public, max-age=86400",
	})
}
