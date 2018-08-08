package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strings"

	"github.com/mattermost/mattermost-server/model"
	"github.com/pelletier/go-toml"
)

var client *model.Client4
var webSocketClient *model.WebSocketClient

var botUser *model.User
var botTeam *model.Team
var debuggingChannel *model.Channel

type Config struct {
	host         string
	botName      string
	channelName  string
	teamName     string
	userLogin    string
	userPassword string
}

//LoadConfig load the toml file into the lib
func LoadConfig(ConfigPath string) *toml.Tree {
	config, err := toml.LoadFile(ConfigPath)
	if err != nil {
		fmt.Printf("%s %s\n", ConfigPath, "is unreadable (check the file path or lint it on https://www.tomllint.com)")
		os.Exit(1)
	}
	return config
}

//ParseConfig set the Config object with the actual value of the toml
func ParseConfig(config *toml.Tree) Config {
	keysArray := config.Keys()
	if StringInSlice("general", keysArray) == false {
		fmt.Printf("%s %s\n", "The config file don't get any [general] section in", &configPath)
		os.Exit(1)
	}
	conf := Config{
		host:         config.Get("mattermost.host").(string),
		botName:      config.Get("general.bot_name").(string),
		channelName:  config.Get("mattermost.channel_name").(string),
		teamName:     config.Get("mattermost.team_name").(string),
		userLogin:    config.Get("mattermost.user_login").(string),
		userPassword: config.Get("mattermost.user_password").(string),
	}
	return conf
}

func MakeSureServerIsRunning() {
	if props, resp := client.GetOldClientConfig(""); resp.Error != nil {
		println("There was a problem pinging the Mattermost server.  Are you sure it's running?")
		PrintError(resp.Error)
		os.Exit(1)
	} else {
		println("Server detected and is running version " + props["Version"])
	}
}

func LoginAsTheBotUser(USER_EMAIL string, USER_PASSWORD string) {
	if user, resp := client.Login(USER_EMAIL, USER_PASSWORD); resp.Error != nil {
		println("There was a problem logging into the Mattermost server.  Are you sure ran the setup steps from the README.md?")
		PrintError(resp.Error)
		os.Exit(1)
	} else {
		botUser = user
	}
}

func FindBotTeam(TEAM_NAME string) {
	if team, resp := client.GetTeamByName(TEAM_NAME, ""); resp.Error != nil {
		println("We failed to get the initial load")
		println("or we do not appear to be a member of the team '" + TEAM_NAME + "'")
		PrintError(resp.Error)
		os.Exit(1)
	} else {
		botTeam = team
	}
}

func CreateBotDebuggingChannelIfNeeded(CHANNEL_LOG_NAME string) {
	if rchannel, resp := client.GetChannelByName(CHANNEL_LOG_NAME, botTeam.Id, ""); resp.Error != nil {
		println("We failed to get the channels")
		PrintError(resp.Error)
	} else {
		debuggingChannel = rchannel
		return
	}

	// Looks like we need to create the logging channel
	channel := &model.Channel{}
	channel.Name = CHANNEL_LOG_NAME
	channel.DisplayName = "Debugging For Sample Bot"
	channel.Purpose = "This is used as a test channel for logging bot debug messages"
	channel.Type = model.CHANNEL_OPEN
	channel.TeamId = botTeam.Id
	if rchannel, resp := client.CreateChannel(channel); resp.Error != nil {
		println("We failed to create the channel " + CHANNEL_LOG_NAME)
		PrintError(resp.Error)
	} else {
		debuggingChannel = rchannel
		println("Looks like this might be the first run so we've created the channel " + CHANNEL_LOG_NAME)
	}
}

func SendMsgToDebuggingChannel(msg string, replyToId string) {
	post := &model.Post{}
	post.ChannelId = debuggingChannel.Id
	post.Message = msg

	post.RootId = replyToId

	if _, resp := client.CreatePost(post); resp.Error != nil {
		println("We failed to send a message to the logging channel")
		PrintError(resp.Error)
	}
}

func HandleWebSocketResponse(event *model.WebSocketEvent) {
	HandleMsgFromDebuggingChannel(event)
}

func HandleMsgFromDebuggingChannel(event *model.WebSocketEvent) {
	// If this isn't the debugging channel then lets ingore it
	if event.Broadcast.ChannelId != debuggingChannel.Id {
		return
	}

	// Lets only reponded to messaged posted events
	if event.Event != model.WEBSOCKET_EVENT_POSTED {
		return
	}

	post := model.PostFromJson(strings.NewReader(event.Data["post"].(string)))
	if post != nil {

		// ignore my events
		if post.UserId == botUser.Id {
			return
		}

		if matched, _ := regexp.MatchString(KubeWord, post.Message); matched {
			words := strings.Fields(post.Message)
			cmd := CheckBeforeExec(words, post.Message)
			cmdOut := ExecKubectl(cmd)
			if cmdOut != "null" && len(cmdOut) > 0 {
				println("responding to -> ", post.Message)
				SendMsgToDebuggingChannel(cmdOut, post.Id)
				return
			}
		}

		// if you see any word matching 'alive' then respond
		if matched, _ := regexp.MatchString(`(?:^|\W)alive(?:$|\W)`, post.Message); matched {
			SendMsgToDebuggingChannel("Yes I'm running", post.Id)
			return
		}

		if matched, _ := regexp.MatchString(`(?:^|\W)help(?:$|\W)`, post.Message); matched {
			SendMsgToDebuggingChannel("!k [namespace] verb [ressource]", post.Id)
			return
		}

		// if you see any word matching 'up' then respond
		if matched, _ := regexp.MatchString(`(?:^|\W)up(?:$|\W)`, post.Message); matched {
			SendMsgToDebuggingChannel("Yes I'm running", post.Id)
			return
		}

		// if you see any word matching 'running' then respond
		if matched, _ := regexp.MatchString(`(?:^|\W)running(?:$|\W)`, post.Message); matched {
			SendMsgToDebuggingChannel("Yes I'm running", post.Id)
			return
		}

		// if you see any word matching 'hello' then respond
		if matched, _ := regexp.MatchString(`(?:^|\W)Hello(?:$|\W)`, post.Message); matched {
			SendMsgToDebuggingChannel("Hello my friend !", post.Id)
			return
		}
	}

	//SendMsgToDebuggingChannel("I did not understand you!", post.Id)
}

func PrintError(err *model.AppError) {
	println("\tError Details:")
	println("\t\t" + err.Message)
	println("\t\t" + err.Id)
	println("\t\t" + err.DetailedError)
}

func SetupGracefulShutdown(BOT_NAME string) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go func() {
		for _ = range c {
			if webSocketClient != nil {
				webSocketClient.Close()
			}

			SendMsgToDebuggingChannel("_"+BOT_NAME+" has **stopped** running_", "")
			os.Exit(0)
		}
	}()
}

//StringInSlice check if a string is in a []string
func StringInSlice(a string, list []string) bool {
	for _, b := range list {
		if b == a {
			return true
		}
	}
	return false
}

// CheckBeforeExec - Check stuffs before exec.
func CheckBeforeExec(words []string, lastmsg string) string {
	cmd := "null"
	if words[0] == KubeWord && len(words) >= 3 {
		cmd = strings.Replace(lastmsg, KubeWord, "/usr/local/bin/kubectl -n", -1)

		// If it contain "all" namespace
		if words[1] == "all" {
			cmd = cmd + " --all-namespaces"
		}

		if !StringInSlice(words[2], ValidVerbs) {
			fmt.Printf("-> Error, command unavailable %+v \n", cmd)
			cmd = "null"
		}
		// Match TRUSTED words (get, scale ...)
		if words[2] == "logs" && StringInSlice("-f", words) {
			fmt.Printf("-> Error, command unavailable %+v \n", cmd)
			cmd = "null"
		}
		if words[2] == "exec" && StringInSlice("-it", words) {
			fmt.Printf("-> Error, command unavailable %+v \n", cmd)
			cmd = "null"
		}
	}
	return cmd
}

// ExecKubectl - Launch and format kubectl cmd.
func ExecKubectl(cmd string) string {
	var cl string
	args := strings.Split(cmd, " ")
	out, err := exec.Command(args[0], args[1:]...).Output()
	if err == nil {
		result := fmt.Sprintf("``` \n %s ```", out)
		cl = strings.Replace(result, "\n\n", "\n", -1)
	}
	return cl
}
