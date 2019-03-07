package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
)

type theConfig struct {
	Version    string
	Slacktoken string
}

type emojiPayload struct {
	Token string
}

func main() {
	var config theConfig

	config.Version = "0.3"

	//slackhook := flag.String("slackhook", "", "Slack Webhook")
	slacktoken := flag.String("slacktoken", "", "Slack Bot Token")
	version := flag.Bool("v", false, "Tagger Version")

	flag.Parse()

	if *version {
		fmt.Println("emojipounder" + config.Version)
		os.Exit(0)
	}

	config.Slacktoken = *slacktoken
}

func emojiAdd(config theConfig, payload emojiPayload) error {
	url := "https://slack.com/api/emoji.add"

	payload.Token = config.Slacktoken

	jsonStr, err := json.Marshal(&payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonStr))
	if err != nil {
		return err
	}

	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("Authorization", "Bearer "+config.Slacktoken)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return err
}

// emojme-add
//var addOptions = {
//    src: ['./emoji1.jpg', 'http://example.com/emoji2.png'], // upload these two images
//    name: ['myLocalEmoji', 'myOnlineEmoji'], // call them these two names
//    bustCache: false, // don't bother redownloading existing emoji
//    avoidCollisions: true, // if there are similarly named emoji, change my new emoji names
//    output: false // don't write any files
//};
//  var subdomains = ['mySubdomain1', 'mySubdomain2'] // can add one or multiple
//  var tokens = ['myToken1', 'myToken2'] // can add one or multiple
//  var addResults = await emojme.add(subdomains, tokens, addOptions);
//  console.log(addResults);
