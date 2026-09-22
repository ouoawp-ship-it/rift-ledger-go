// dbcheck only reads a snapshot. It never starts HTTP or a Telegram consumer.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"riftledger/internal/app"
	"riftledger/internal/sqlite"
)

func run() error {
	path := flag.String("db", "", "备份数据库文件（只读；不创建文件）")
	flag.Parse()
	if *path == "" || flag.NArg() != 0 {
		return fmt.Errorf("用法：rift-dbcheck -db /backup.db")
	}
	db, err := sqlite.OpenBackup(*path)
	if err != nil {
		return err
	}
	defer db.Close()
	report, err := app.InspectDatabase(db)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err = enc.Encode(report); err != nil {
		return err
	}
	if report["ok"] != true {
		return fmt.Errorf("备份验证未通过；不要恢复此文件")
	}
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
