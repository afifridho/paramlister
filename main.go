package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"time"

	"github.com/go-redis/redis"
	"github.com/kelseyhightower/envconfig"
	"github.com/labstack/echo"
	log "github.com/sirupsen/logrus"
	"github.com/joho/godotenv"
)

type TagList struct {
	Name string `json:"name"`
}

type AppResponse struct {
	StatusCode int      `json:"status"`
	Message    []string `json:"message"`
	Timestamp  int64    `json:"timestamp"`
}

type EnvConfig struct {
	RedisAddress      string `envconfig:"REDIS_ADDRESS" default:"localhost:6379" required:"true"`
	GithubAccessToken string `envconfig:"GITHUB_ACCESS_TOKEN" required:"true"`
	GithubUser        string `envconfig:"GITHUB_USER" required:"true"`
	AppListenAddress  string `envconfig:"APP_LISTEN_ADDRESS" default:"0.0.0.0:8000"`
}

var env EnvConfig

func init() {
	// Log as JSON instead of the default ASCII formatter.
	log.SetFormatter(&log.JSONFormatter{})

	// Output to stdout instead of the default stderr
	// Can be any io.Writer, see below for File example
	log.SetOutput(os.Stdout)

	// Only log the warning severity or above.
	log.SetLevel(log.DebugLevel)

	initEnvVars()
}

func initEnvVars() error {
	if _, err := os.Stat(".env"); os.IsNotExist(err) {
		if err := envconfig.Process("", &env); err != nil {
			log.Fatal(err.Error())
		}
		return nil
	}

	if err := godotenv.Load(".env"); err != nil {
		log.Fatal("failed to read from .env file")
	}

	if err := envconfig.Process("", &env); err != nil {
		log.Fatal(err.Error())
	}

	return nil
}

func createRedisClient() *redis.Client {
	client := redis.NewClient(&redis.Options{
		Addr: env.RedisAddress,
	})

	return client
}

func DeleteRedisKey(key string) {
	client := createRedisClient()
	defer client.Close()

	err := client.Del(key).Err()
	if err != nil {
		panic(err)
	}

	log.Debugf("key %v has been deleted", key)
}

func setListOfValueToRedis(key string, values []string) {
	client := createRedisClient()
	defer client.Close()

	for _, value := range values {
		err := client.RPush(key, value).Err()
		if err != nil {
			panic(err)
		}
	}

	log.Debugf("key %v has been set with value %v", key, values)
}

func getListOfValueFromRedis(key string) []string {
	client := createRedisClient()

	defer client.Close()

	value, err := client.LRange(key, 0, -1).Result()
	if err == redis.Nil {
		log.Warnf("key %v does not exist", key)
	} else if err != nil {
		panic(err)
	} else {
		log.Debugf("key %v has value %v", key, value)
	}

	return value
}

func queryToGithub(ctx context.Context, projectName string, ch chan string) {

	url := fmt.Sprintf("https://api.github.com/repos/%v/%v/tags", env.GithubUser, projectName)
	method := "GET"

	client := &http.Client{}
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		log.Println(err)
	}
	req.Header.Add("Authorization", fmt.Sprintf("token %v", env.GithubAccessToken))

	if err != nil {
		fmt.Println(err)
	}
	req.Header.Add("Accept", "application/vnd.github.v3.raw")

	res, err := client.Do(req)
	if err != nil {
		log.Println(err)
	}
	log.Debugf("%v method request has been sent to %v", method, url)

	defer res.Body.Close()

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		log.Println(err)
	}

	ch <- string(body)
}

func hello(c echo.Context) error {
	return c.String(http.StatusOK, "Hello, World!")
}

func healthCheck(c echo.Context) error {
	client := createRedisClient()
	defer client.Close()

	pong, err := client.Ping().Result()
	if err != nil {
		return err
	}

	log.Info(pong)
	return c.String(http.StatusOK, pong)
}

func getGithubTag(c echo.Context) error {
	repoName := c.Param("reponame")

	versionArray := []string{}

	versionArray = getListOfValueFromRedis(repoName)
	if len(versionArray) > 0 {
		response := &AppResponse{
			StatusCode: 200,
			Message:    versionArray,
			Timestamp:  time.Now().Unix(),
		}

		return c.JSONPretty(http.StatusOK, response, "  ")
	}

	ctx := context.TODO()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	ch := make(chan string, 10)

	go queryToGithub(ctx, repoName, ch)

	JSONstring := <-ch

	var releases []TagList
	json.Unmarshal([]byte(JSONstring), &releases)

	for _, name := range releases {
		version := name.Name
		versionArray = append(versionArray, version)
	}

	// DeleteRedisKey(repoName)
	go setListOfValueToRedis(repoName, versionArray)

	response := &AppResponse{
		StatusCode: 200,
		Message:    versionArray,
		Timestamp:  time.Now().Unix(),
	}

	return c.JSONPretty(http.StatusOK, response, "  ")
}

func main() {
	e := echo.New()

	log.SetFormatter(&log.JSONFormatter{})

	e.GET("/", hello)
	e.GET("/health", healthCheck)
	e.GET("/githubtag/:reponame", getGithubTag)
	err := e.Start(env.AppListenAddress)
	if err != nil {
		log.Error(err)
	}
}