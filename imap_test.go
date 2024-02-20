package inbox

import (
	"fmt"
	"github.com/emersion/go-imap"
	"strings"
	"testing"
)

func Test(t *testing.T) {
	const (
		username = "xxx@gmail.com"
		password = "xxx"
	)

	e := New(GmailConfig())
	t.Log("exec Login ...")
	err := e.Login(username, password)
	if err != nil {
		t.Fatal("Failed to Login:", err)
	}

	defer e.Release()
	//err = e.SendMessage(username, username, "Test", "This is test!")
	//if err != nil {
	//	t.Fatal("Failed to Send:", err)
	//}

	t.Log("exec Search ...")
	criteria := imap.NewSearchCriteria()
	criteria.WithoutFlags = []string{imap.SeenFlag}
	//criteria.Since = time.Now().Add(-7 * time.Hour * 24)
	criteria.Header.Set("To", "@1micro.top")
	subjects, err := e.RecvMessage("INBOX", false, criteria)
	if err != nil {
		t.Fatal("Failed to Recv:", err)
	}

	for _, subject := range subjects {
		if !Contains(subject.To, "@1micro.top") {
			continue
		}
		// 打印邮件的内容
		fmt.Println("====================================")
		fmt.Println("Received test email successfully.")
		fmt.Println("From:", subject.From)
		fmt.Println("To:", subject.To)
		fmt.Println("Subject:", subject.Title)
		fmt.Println("Text:")
		fmt.Println(subject.Content)
		fmt.Print("====================================\n\n\n")
	}
}

func Contains(to []string, child string) bool {
	for _, value := range to {
		if strings.Contains(value, child) {
			return true
		}
	}
	return false
}
