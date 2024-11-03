package main

import (
	"context"
	"flag"
	"fmt"
	"log"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

type Instance struct {
	GlobalIP        string
	InstanceId      string
	Name            string
	Platform        string
	PrivateIp       string
	SecurityGroupId string
	State           string
}

type EC2Client struct {
	client *ec2.Client
	ctx    context.Context
}

func NewEC2Client(profile string) (*EC2Client, error) {
	ctx := context.TODO()
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithSharedConfigProfile(profile),
		config.WithRegion("ap-northeast-1"),
	)
	if err != nil {
		return nil, fmt.Errorf("unable to load SDK config: %v", err)
	}

	return &EC2Client{
		client: ec2.NewFromConfig(cfg),
		ctx:    ctx,
	}, nil
}

func (c *EC2Client) getInstances() ([]Instance, error) {
	input := &ec2.DescribeInstancesInput{}
	result, err := c.client.DescribeInstances(c.ctx, input)
	if err != nil {
		return nil, err
	}

	var instances []Instance
	for _, reservation := range result.Reservations {
		for _, instance := range reservation.Instances {
			var name string
			for _, tag := range instance.Tags {
				if *tag.Key == "Name" {
					name = *tag.Value
					break
				}
			}

			// Platform の取得（Windowsかどうか）
			platform := "None"
			if instance.Platform != "" {
				platform = string(instance.Platform)
			}

			// Public IP の取得
			var publicIP string
			if instance.PublicIpAddress != nil {
				publicIP = *instance.PublicIpAddress
			}

			// Private IP の取得
			var privateIP string
			if len(instance.NetworkInterfaces) > 0 && instance.NetworkInterfaces[0].PrivateIpAddress != nil {
				privateIP = *instance.NetworkInterfaces[0].PrivateIpAddress
			}

			// Security Group IDs の取得
			var sgIDs []string
			for _, sg := range instance.SecurityGroups {
				sgIDs = append(sgIDs, *sg.GroupId)
			}

			instances = append(instances, Instance{
				GlobalIP:        publicIP,
				InstanceId:      *instance.InstanceId,
				Name:            name,
				Platform:        platform,
				PrivateIp:       privateIP,
				SecurityGroupId: joinStrings(sgIDs, ", "),
				State:           string(instance.State.Name),
			})
		}
	}

	return instances, nil
}

func (c *EC2Client) startInstance(instanceID string) error {
	input := &ec2.StartInstancesInput{
		InstanceIds: []string{instanceID},
	}
	_, err := c.client.StartInstances(c.ctx, input)
	return err
}

func (c *EC2Client) stopInstance(instanceID string) error {
	input := &ec2.StopInstancesInput{
		InstanceIds: []string{instanceID},
	}
	_, err := c.client.StopInstances(c.ctx, input)
	return err
}

func joinStrings(slice []string, sep string) string {
	if len(slice) == 0 {
		return ""
	}
	result := slice[0]
	for i := 1; i < len(slice); i++ {
		result += sep + slice[i]
	}
	return result
}

func main() {
	// コマンドライン引数の処理
	profile := flag.String("profile", "default", "AWS profile name")
	flag.Parse()

	// EC2クライアントの初期化
	ec2Client, err := NewEC2Client(*profile)
	if err != nil {
		log.Fatalf("Failed to create EC2 client: %v", err)
	}

	app := tview.NewApplication()
	table := tview.NewTable().SetSelectable(true, false)

	// ヘッダーの設定
	headers := []string{"GlobalIP", "InstanceId", "Name", "Platform", "PrivateIp", "SecurityGroupId", "State"}
	for i, header := range headers {
		table.SetCell(0, i,
			tview.NewTableCell(header).
				SetTextColor(tcell.ColorYellow).
				SetSelectable(false))
	}

	refreshTable := func(table *tview.Table) error {
		instances, err := ec2Client.getInstances()
		if err != nil {
			return err
		}

		// 既存のデータをクリア (ヘッダー以外)
		for row := table.GetRowCount() - 1; row >= 1; row-- {
			table.RemoveRow(row)
		}

		// 新しいデータの設定
		for i, instance := range instances {
			row := i + 1
			table.SetCell(row, 0, tview.NewTableCell(instance.GlobalIP))
			table.SetCell(row, 1, tview.NewTableCell(instance.InstanceId))
			table.SetCell(row, 2, tview.NewTableCell(instance.Name))
			table.SetCell(row, 3, tview.NewTableCell(instance.Platform))
			table.SetCell(row, 4, tview.NewTableCell(instance.PrivateIp))
			table.SetCell(row, 5, tview.NewTableCell(instance.SecurityGroupId))

			// インスタンスの状態に応じて色を変更
			stateCell := tview.NewTableCell(instance.State)
			switch instance.State {
			case string(types.InstanceStateNameRunning):
				stateCell.SetTextColor(tcell.ColorGreen)
			case string(types.InstanceStateNameStopped):
				stateCell.SetTextColor(tcell.ColorRed)
			case string(types.InstanceStateNamePending), string(types.InstanceStateNameStopping):
				stateCell.SetTextColor(tcell.ColorYellow)
			}
			table.SetCell(row, 6, stateCell)
		}

		return nil
	}

	// 初期データの読み込み
	if err := refreshTable(table); err != nil {
		log.Fatal(err)
	}

	// キーバインドの設定
	table.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		row, _ := table.GetSelection()
		if row == 0 { // ヘッダー行は無視
			return event
		}

		instanceID := table.GetCell(row, 1).Text
		switch event.Key() {
		case tcell.KeyRune:
			switch event.Rune() {
			case 's': // 起動
				if err := ec2Client.startInstance(instanceID); err != nil {
					showMessage(app, table, fmt.Sprintf("Error starting instance: %v", err))
				} else {
					showMessage(app, table, fmt.Sprintf("Starting instance: %s", instanceID))
				}
			case 't': // 停止
				if err := ec2Client.stopInstance(instanceID); err != nil {
					showMessage(app, table, fmt.Sprintf("Error stopping instance: %v", err))
				} else {
					showMessage(app, table, fmt.Sprintf("Stopping instance: %s", instanceID))
				}
			case 'q': // 終了
				app.Stop()
			case 'r': // リフレッシュ
				if err := refreshTable(table); err != nil {
					showMessage(app, table, fmt.Sprintf("Error refreshing: %v", err))
				}
			}
		default:
			// 何もしない
		}
		return event
	})

	// 使い方の説明を追加
	help := tview.NewTextView().
		SetText(fmt.Sprintf("[Profile: %s]  Keys: [s] Start  [t] Stop  [r] Refresh  [q] Quit", *profile)).
		SetTextColor(tcell.ColorGreen)

	// レイアウトの設定
	flex := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(table, 0, 1, true).
		AddItem(help, 1, 1, false)

	if err := app.SetRoot(flex, true).EnableMouse(true).Run(); err != nil {
		log.Fatal(err)
	}
}

func showMessage(app *tview.Application, table *tview.Table, message string) {
	modal := tview.NewModal().
		SetText(message).
		AddButtons([]string{"OK"}).
		SetDoneFunc(func(buttonIndex int, buttonLabel string) {
			app.SetRoot(table, true)
		})

	app.SetRoot(modal, false)
}
