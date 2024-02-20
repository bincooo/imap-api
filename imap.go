package inbox

import (
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/smtp"
	"strings"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
)

type Config struct {
	SmtpPort   int
	SmtpServer string
	ImapPort   int
	ImapServer string
}

type Email struct {
	Config
	send *smtp.Client
	recv *client.Client
}

type Subject struct {
	From        []string
	To          []string
	Title       string
	Content     string
	HtmlContent string
}

func GmailConfig() Config {
	return Config{
		SmtpPort:   587,
		SmtpServer: "smtp.gmail.com",
		ImapPort:   993,
		ImapServer: "imap.gmail.com",
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
			InsecureSkipVerify: true,
		}); err != nil {
			return err
		}

		// 登录您的账号和密码
		if err = send.Auth(smtp.PlainAuth("", user, passwd, e.SmtpServer)); err != nil {
			return err
		}
		e.send = send
	}

	// 接收端
	{
		// 创建一个 IMAP 客户端
		recv, err := client.DialTLS(e.imapAdder(), &tls.Config{
			InsecureSkipVerify: true,
		})
		if err != nil {
			e.Release()
			return err
		}

		// 登录您的账号和密码
		if err = recv.Login(user, passwd); err != nil {
			e.Release()
			return err
		}
		e.recv = recv
	}

	return nil
}

// 退出登陆
func (e *Email) Release() {
	if e.send != nil {
		_ = e.send.Close()
	}
	if e.recv != nil {
		_ = e.recv.Logout()
	}
}

func (e *Email) RecvMessage(box string, readOnly bool, criteria *imap.SearchCriteria) ([]Subject, error) {
	if e.recv == nil {
		return nil, errors.New("do it after login")
	}

	mbox, err := e.recv.Select(box, readOnly)

	// 搜索您想要获取的邮件，例如最新的一封
	ids, err := e.recv.Search(criteria)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, errors.New("no mail")
	}

	maxLen := 10
	if l := len(ids); l < maxLen {
		maxLen = l
	}

	// 获取邮件的内容
	section := &imap.BodySectionName{Peek: true}
	items := []imap.FetchItem{section.FetchItem(), imap.FetchEnvelope, imap.FetchBodyStructure}
	messages := make(chan *imap.Message, maxLen)
	done := make(chan error, 1)
	go func() {
		s := new(imap.SeqSet)
		s.AddNum(ids...)
		s.AddRange(mbox.Messages-uint32(maxLen), mbox.Messages)
		done <- e.recv.Fetch(s, items, messages)
	}()

	Address := func(iter []*imap.Address) []string {
		var r []string
		for _, o := range iter {
			r = append(r, o.Address())
		}
		return r
	}

	var subjects []Subject
	// 读取邮件的内容
	for {
		msg := <-messages
		if msg == nil {
			break
		}
		body := msg.GetBody(section)
		if body == nil {
			fmt.Println("Server didn't return message body")
			break
		}

		// readBody := Read(body)

		// 获取邮件的内容类型
		var (
			content     []string
			htmlContent []string
		)
		if msg.BodyStructure != nil {
			// 获取邮件的内容类型
			contentType := mime.FormatMediaType(msg.BodyStructure.MIMEType, msg.BodyStructure.Params)
			mediaType, params, _ := mime.ParseMediaType(contentType)
			if mediaType == "multipart" {
				reader := multipart.NewReader(body, params["boundary"])
				for {
					part, er := reader.NextPart()
					if er == io.EOF {
						break
					}
					partEncoding := part.Header.Get("Content-Transfer-Encoding")
					mime := part.Header.Get("Content-Type")
					var result string
					if partEncoding == "base64" {
						// 创建一个 base64 解码器
						decoder := base64.NewDecoder(base64.StdEncoding, part)
						data, _ := io.ReadAll(decoder)
						result = string(data)
					} else {
						data, _ := io.ReadAll(part)
						result = string(data)
					}

					if strings.Contains(mime, "text/html") {
						htmlContent = append(htmlContent, result)
					} else {
						content = append(content, result)
					}
				}
			} else {
				mime := msg.BodyStructure.MIMESubType
				var result string
				if msg.BodyStructure.Encoding == "base64" {
					decoder := base64.NewDecoder(base64.StdEncoding, body)
					// 读取解码后的内容
					data, _ := io.ReadAll(decoder)
					result = string(data)
				} else {
					data, _ := io.ReadAll(body)
					result = string(data)
				}

				index := strings.Index(result, "\r\n\r\n")
				if index > 0 {
					result = result[index:]
				}

				if mime == "html" {
					htmlContent = append(htmlContent, result)
				} else {
					content = append(content, result)
				}
			}
		}

		subjects = append(subjects, Subject{
			Address(msg.Envelope.From),
			Address(msg.Envelope.To),
			msg.Envelope.Subject,
			strings.Join(content, "\n\n"),
			strings.Join(htmlContent, "\n\n"),
		})

		// 标记邮件为已读
		store := e.recv.Store
		seqSet := new(imap.SeqSet)
		seqSet.AddNum(msg.SeqNum)
		storeItems := imap.FormatFlagsOp(imap.AddFlags, true)
		storeFlags := []interface{}{imap.SeenFlag}
		if err = store(seqSet, storeItems, storeFlags, nil); err != nil {
			return subjects, err
		}
	}

	<-done
	return subjects, nil
}

func (e *Email) SendMessage(from, to, subject, body string) error {
	if e.send == nil {
		return errors.New("do it after login")
	}

	// 发送一封邮件
	header := make(map[string]string)
	header["From"] = from
	header["To"] = to
	header["Subject"] = subject
	header["Content-Type"] = "text/plain; charset=UTF-8"
	var message strings.Builder
	for k, v := range header {
		message.WriteString(fmt.Sprintf("%s: %s\r\n", k, v))
	}
	message.WriteString("\r\n")
	message.WriteString(body)

	if err := e.send.Mail(from); err != nil {
		return err
	}

	if err := e.send.Rcpt(to); err != nil {
		return err
	}

	writer, err := e.send.Data()
	if err != nil {
		return err
	}
	defer writer.Close()

	if _, err = writer.Write([]byte(message.String())); err != nil {
		return err
	}
	return nil
}
