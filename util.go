package manor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"
)

func PostTxt(client *http.Client, url string, v string) (string, error) {
	body := bytes.NewBufferString(v)
	req, err := http.NewRequest("POST", url, body)
	req.Header.Add("Content-Type", "text/plain;charset=utf-8")
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	content, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(content), nil
}

func PostJson(client *http.Client, url string, v interface{}) (string, error) {
	b, err := json.Marshal(v)
	body := bytes.NewBuffer([]byte(b))
	req, err := http.NewRequest("POST", url, body)
	req.Header.Add("Content-Type", "application/json;charset=utf-8")
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	content, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(content), nil
}

func Ts() string {
	t := time.Now()
	ts := fmt.Sprintf("%d%02d%02d_%02d%02d",
		t.Year(), t.Month(), t.Day(),
		t.Hour(), t.Minute())
	return ts
}
