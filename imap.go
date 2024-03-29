package inbox

import (
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net/smtp"
	"strings"

	"github.com/emersion/go-imap/v2"
	client "github.com/emersion/go-imap/v2/imapclient"
)

type Config struct {
	SmtpPort   int
	SmtpServer string
	ImapPort   int
	ImapServer string
}

type Email struct {
	Config
	sc *smtp.Client
	rc *client.Client
}

type Subject struct {
	From    []string // 发送者
	To      []string // 接收者
	Title   string   // 标题
	Content string   // 内容
}

func GmailConfig() Config {
	return Config{
		SmtpPort:   587,
		SmtpServer: "smtp.gmail.com",
		ImapPort:   993,
		ImapServer: "imap.gmail.com",
	}
}

func OutlookConfig() Config {
	return Config{
		SmtpPort:   587,
		SmtpServer: "smtp.office365.com",
		ImapPort:   993,
		ImapServer: "outlook.office365.com",
	}
}

func QMailConfig() Config {
	return Config{
		SmtpPort:   587,
		SmtpServer: "smtp.qq.com",
		ImapPort:   993,
		ImapServer: "imap.qq.com",
	}
}

func New(c Config) Email {
	return Email{c, nil, nil}
}

func (c Config) smtpAdder() string {
	return fmt.Sprintf("%s:%d", c.SmtpServer, c.SmtpPort)
}

func (c Config) imapAdder() string {
	return fmt.Sprintf("%s:%d", c.ImapServer, c.ImapPort)
}

// 登陆邮箱
func (e *Email) Login(user, passwd string) error {
	// 发送端
	{
		// 创建一个 SMTP 客户端
		send, err := smtp.Dial(e.smtpAdder())
		if err != nil {
			return err
		}

		// 启用 TLS 加密
		if err = send.StartTLS(&tls.Config{
			ServerName:         e.SmtpServer,
			InsecureSkipVerify: true,
		}); err != nil {
			return err
		}

		// 登录您的账号和密码
		if err = send.Auth(smtp.PlainAuth("", user, passwd, e.SmtpServer)); err != nil {
			return err
		}
		e.sc = send
	}

	// 接收端
	{
		// 创建一个 IMAP 客户端
		recv, err := client.DialTLS(e.imapAdder(), nil)
		if err != nil {
			e.Release()
			return err
		}

		// 登录您的账号和密码
		cmd := recv.Login(user, passwd)
		if err = cmd.Wait(); err != nil {
			e.Release()
			return err
		}
		e.rc = recv
	}

	return nil
}

// 退出登陆
func (e *Email) Release() {
	if e.sc != nil {
		_ = e.sc.Close()
		e.sc = nil
	}
	if e.rc != nil {
		_ = e.rc.Logout()
		e.rc = nil
	}
}

func (e *Email) RecvMessage(box string, readOnly bool, criteria *imap.SearchCriteria) ([]Subject, error) {
	if e.rc == nil {
		return nil, errors.New("do it after login")
	}

	cmd := e.rc.Select(box, &imap.SelectOptions{
		ReadOnly: readOnly,
	})

	selectRows, err := cmd.Wait()
	if err != nil {
		return nil, err
	}

	numMessages := int(selectRows.NumMessages)
	if numMessages == 0 {
		return nil, errors.New("no mail")
	}

	// 搜索您想要获取的邮件
	searchRows, err := e.rc.Search(criteria, nil).Wait()
	if err != nil {
		return nil, err
	}

	ids := searchRows.AllSeqNums()
	numMessages = len(ids)
	if numMessages == 0 {
		return nil, errors.New("no mail")
	}

	// 只读10条
	var maxLen = 10
	if numMessages < maxLen {
		maxLen = numMessages
	}

	limit := imap.SeqSetNum(ids[numMessages-maxLen:]...)
	var messages []*client.FetchMessageBuffer

	// 获取邮件的内容
	fetchOptions := &imap.FetchOptions{
		Flags:         true,
		Envelope:      true,
		BodyStructure: &imap.FetchItemBodyStructure{Extended: true},
		BodySection: []*imap.FetchItemBodySection{
			{
				Specifier: imap.PartSpecifierHeader,
			},
			{
				Part: []int{1, 2, 3},
			},
		},
	}

	// 获取数据
	fetchCmd := e.rc.Fetch(limit, fetchOptions)
	if messages, err = fetchCmd.Collect(); err != nil {
		return nil, err
	}

	var subjects []Subject
	for _, messageBuffer := range messages {
		{
			content, er := messageString(messageBuffer)
			if er != nil {
				return subjects, er
			}

			subjects = append(subjects, Subject{
				From:    addrStrings(messageBuffer.Envelope.From),
				To:      addrStrings(messageBuffer.Envelope.To),
				Title:   messageBuffer.Envelope.Subject,
				Content: content,
			})
		}

		// 标记邮件为已读
		store := e.rc.Store
		seqSet := imap.SeqSetNum(messageBuffer.SeqNum)
		storeItems := imap.StoreFlags{
			Op: imap.StoreFlagsAdd,
			Flags: []imap.Flag{
				imap.FlagSeen,
			},
		}
		storeCmd := store(seqSet, &storeItems, nil)
		if er := storeCmd.Wait(); er != nil {
			return subjects, er
		}
	}

	return subjects, nil
}

func messageString(messageBuffer *client.FetchMessageBuffer) (string, error) {
	if messageBuffer == nil {
		return "", nil
	}

	multiPart, ok := messageBuffer.BodyStructure.(*imap.BodyStructureMultiPart)
	if ok {
		index := 0
		for _, section := range messageBuffer.BodySection {
			if index == 1 {
				structure, ok := multiPart.Children[0].(*imap.BodyStructureSinglePart)
				if ok {
					if structure.Encoding == "BASE64" {
						decodeBytes, err := base64.StdEncoding.DecodeString(string(section))
						if err != nil {
							return "", err
						}
						return string(decodeBytes), nil
					}
					return string(section), nil
				}
			}
			index++
		}
	}

	singlePart, ok := messageBuffer.BodyStructure.(*imap.BodyStructureSinglePart)
	if ok {
		index := 0
		for _, section := range messageBuffer.BodySection {
			if index == 1 {
				if singlePart.Encoding == "BASE64" {
					decodeBytes, err := base64.StdEncoding.DecodeString(string(section))
					if err != nil {
						return "", err
					}
					return string(decodeBytes), nil
				}
				return string(section), nil
			}
			index++
		}
	}

	return "", nil
}

func (e *Email) SendMessage(from, to, subject, body string) error {
	if e.sc == nil {
		return errors.New("please login")
	}
	return e.sendMessage("text/plain", from, to, subject, body)
}

func (e *Email) SendHtmlMessage(from, to, subject, body string) error {
	if e.sc == nil {
		return errors.New("please login")
	}
	return e.sendMessage("text/html", from, to, subject, body)
}

func (e *Email) sendMessage(contentType, from, to, subject, body string) error {
	// 发送一封邮件
	header := make(map[string]string)
	header["From"] = from
	header["To"] = to
	header["Subject"] = subject
	header["Content-Type"] = contentType + "; charset=UTF-8"
	var message strings.Builder
	for k, v := range header {
		message.WriteString(fmt.Sprintf("%s: %s\r\n", k, v))
	}
	message.WriteString("\r\n")
	message.WriteString(body)

	if err := e.sc.Mail(from); err != nil {
		return err
	}

	if err := e.sc.Rcpt(to); err != nil {
		return err
	}

	w, err := e.sc.Data()
	if err != nil {
		return err
	}
	defer w.Close()

	if _, err = w.Write([]byte(message.String())); err != nil {
		return err
	}
	return nil
}

func addrStrings(addr []imap.Address) []string {
	var buf []string
	for _, value := range addr {
		buf = append(buf, value.Addr())
	}
	return buf
}
