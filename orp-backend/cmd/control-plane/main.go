package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	"net.daoke/orp-backend/internal/config"
	"net.daoke/orp-backend/internal/httpapi"
	"net.daoke/orp-backend/internal/store"
)

var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	if err := runCLI(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "orp:", err)
		os.Exit(1)
	}
}

func runServer() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	configuration, err := config.Load()
	if err != nil {
		return err
	}
	database, err := store.Open(configuration.MySQLDSN)
	if err != nil {
		return err
	}
	defer database.Close()
	if err := store.Migrate(database); err != nil {
		return err
	}
	if err := store.SeedAdmin(database); err != nil {
		return err
	}
	if err := store.SeedSettingGroups(database); err != nil {
		return err
	}
	if err := store.SeedRuntimeSettings(database); err != nil {
		return err
	}
	runtimeSettings, err := config.LoadRuntimeSettings(ctx, database)
	if err != nil {
		return err
	}
	configuration, err = runtimeSettings.Apply(configuration)
	if err != nil {
		return err
	}
	if err := httpapi.RecoverPendingPublishes(database); err != nil {
		return err
	}

	httpapi.StartNodeSampler(ctx, database)
	httpapi.StartTLSExpiryAlertScheduler(ctx, database)
	// Candidate jobs reuse the fixed local Quickstart validator and the Agent's
	// allowlisted validate operation. Neither executor activates configuration.
	httpapi.StartCandidateValidationWorker(ctx, database, httpapi.NewCandidateValidationExecutor(database))

	redisClient := redis.NewClient(&redis.Options{
		Addr: configuration.RedisAddress, Password: configuration.RedisPassword, DB: configuration.RedisDB,
		DialTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 2 * time.Second,
	})
	redisCtx, cancelRedis := context.WithTimeout(ctx, 2*time.Second)
	redisErr := redisClient.Ping(redisCtx).Err()
	cancelRedis()
	if redisErr != nil {
		log.Printf("Redis unavailable; dashboard will use direct snapshots: %v", redisErr)
		_ = redisClient.Close()
		redisClient = nil
	} else {
		defer redisClient.Close()
	}
	httpapi.StartKafkaLogConsumer(ctx, database, httpapi.KafkaLogConfig{
		Brokers: configuration.KafkaBrokers,
		Topic:   configuration.KafkaTopic,
		GroupID: configuration.KafkaConsumerID,
	})

	server := &http.Server{
		Addr: configuration.HTTPAddress, Handler: httpapi.NewWithRuntimeSettings(database, redisClient, runtimeSettings),
		ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second,
	}
	var agentServer *http.Server
	if configuration.AgentHTTPSAddress != "" {
		caPEM, err := os.ReadFile(configuration.AgentClientCAFile)
		if err != nil {
			return fmt.Errorf("read Agent client CA: %w", err)
		}
		clientCAs := x509.NewCertPool()
		if !clientCAs.AppendCertsFromPEM(caPEM) {
			return errors.New("Agent client CA contains no certificates")
		}
		agentServer = &http.Server{
			Addr: configuration.AgentHTTPSAddress, Handler: httpapi.NewAgentMux(database),
			ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
			WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second,
			TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, ClientAuth: tls.VerifyClientCertIfGiven, ClientCAs: clientCAs},
		}
	}

	serverErrors := make(chan error, 2)
	go func() {
		log.Printf("orp listening on %s", configuration.HTTPAddress)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- fmt.Errorf("HTTP listener: %w", err)
		}
	}()
	if agentServer != nil {
		go func() {
			log.Printf("orp Agent mTLS listener on %s", configuration.AgentHTTPSAddress)
			if err := agentServer.ListenAndServeTLS(configuration.TLSCertFile, configuration.TLSKeyFile); err != nil && !errors.Is(err, http.ErrServerClosed) {
				serverErrors <- fmt.Errorf("Agent HTTPS listener: %w", err)
			}
		}()
	}

	select {
	case err := <-serverErrors:
		cancel()
		return shutdownServers(server, agentServer, err)
	case <-ctx.Done():
		return shutdownServers(server, agentServer, nil)
	}
}

func shutdownServers(server, agentServer *http.Server, cause error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var shutdownErr error
	if err := server.Shutdown(ctx); err != nil {
		shutdownErr = errors.Join(shutdownErr, fmt.Errorf("shutdown HTTP listener: %w", err))
	}
	if agentServer != nil {
		if err := agentServer.Shutdown(ctx); err != nil {
			shutdownErr = errors.Join(shutdownErr, fmt.Errorf("shutdown Agent listener: %w", err))
		}
	}
	return errors.Join(cause, shutdownErr)
}
