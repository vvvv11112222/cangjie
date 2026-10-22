// model-config prepares the existing main-project model settings without
// connecting to a database, starting a worker, or sending a provider request.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "模型配置："+err.Error())
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("使用 init-pricing、prepare 或 check；这些命令均不会调用模型")
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard) // Never echo an accidental secret supplied as an argument.
	var output, demoEnv, pricingFile, configFile, budget string
	switch args[0] {
	case "init-pricing":
		flags.StringVar(&output, "output", "var/model-setup/pricing.json", "local pricing worksheet")
		flags.StringVar(&budget, "budget", "0", "explicitly approved monthly CNY budget")
	case "prepare":
		flags.StringVar(&demoEnv, "demo-env", "", "explicit source demo .env")
		flags.StringVar(&pricingFile, "pricing", "", "confirmed pricing worksheet")
		flags.StringVar(&output, "output", "var/model-setup/analysis.private.json", "private main-project settings")
	case "check":
		flags.StringVar(&configFile, "config", "", "private main-project settings")
	default:
		return errors.New("未知命令；使用 init-pricing、prepare 或 check")
	}
	if flags.Parse(args[1:]) != nil || flags.NArg() != 0 {
		return errors.New("命令参数不正确；不要在命令行传入密钥")
	}
	if args[0] == "init-pricing" {
		if _, err := money(budget); err != nil {
			return errors.New("budget 必须为非负人民币金额，最多六位小数")
		}
		p := pricing{MonthlyBudget: budget}
		if err := writeNewJSON(output, p); err != nil {
			return err
		}
		_, err := fmt.Fprintln(out, "已创建价格核对表；确认模型、单价、修订和价格来源后再 prepare。未复制密钥，未启用调用。")
		return err
	}
	if args[0] == "check" {
		var settings map[string]string
		if configFile == "" || readJSON(configFile, &settings) != nil {
			return errors.New("无法读取配置 JSON；内容不回显，请检查文件路径和格式")
		}
		summary, err := checkSettings(settings)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(summary)
	}
	if demoEnv == "" || pricingFile == "" {
		return errors.New("prepare 必须指定 -demo-env 和 -pricing；不会自动读取或修改主项目 .env")
	}
	if !strings.HasSuffix(output, ".private.json") {
		return errors.New("密钥配置输出必须以 .private.json 结尾，并保存在本地受控目录")
	}
	var p pricing
	if err := readJSON(pricingFile, &p); err != nil {
		return errors.New("无法读取价格核对表；内容不回显，请检查文件格式")
	}
	// Validate prices before reading the secret source file.
	if err := p.validate(); err != nil {
		return err
	}
	file, err := os.Open(demoEnv)
	if err != nil {
		return errors.New("无法读取指定的 demo 配置文件")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() > 1<<20 {
		return errors.New("demo 配置文件大小不合法")
	}
	demo, err := godotenv.Parse(io.LimitReader(file, 1<<20))
	if err != nil {
		return errors.New("demo 配置文件格式错误；内容不回显")
	}
	settings, err := prepareSettings(demo, p)
	if err != nil {
		return err
	}
	if err := writeNewJSON(output, settings); err != nil {
		return err
	}
	summary, _ := checkSettings(settings)
	return json.NewEncoder(out).Encode(summary)
}

func readJSON(path string, dest any) error {
	file, err := os.Open(path)
	if err != nil {
		return errors.New("cannot read JSON")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() > 1<<20 {
		return errors.New("invalid JSON size")
	}
	dec := json.NewDecoder(io.LimitReader(file, 1<<20))
	dec.DisallowUnknownFields()
	if dec.Decode(dest) != nil {
		return errors.New("invalid JSON")
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return errors.New("extra JSON")
	}
	return nil
}

func writeNewJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return errors.New("无法序列化本地配置")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return errors.New("无法创建本地配置目录")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("无法创建配置：目标可能已存在；不会覆盖旧配置，请使用新文件名")
	}
	_, writeErr := file.Write(append(data, '\n'))
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return errors.New("写入配置失败；请检查本地不完整文件，程序不会自动启用它")
	}
	return nil
}
