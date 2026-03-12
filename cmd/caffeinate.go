package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

const (
	caffeinateLabel = "com.pplx.caffeinate"
	caffeinatePlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.pplx.caffeinate</string>
    <key>ProgramArguments</key>
    <array>
        <string>/usr/bin/caffeinate</string>
        <string>-dimsu</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
</dict>
</plist>
`
)

func caffeinatePlistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", caffeinateLabel+".plist")
}

// isScreenLockOff 检查 sysadminctl 报告的锁屏状态
func isScreenLockOff() bool {
	out, err := exec.Command("sysadminctl", "-screenLock", "status").CombinedOutput()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "screenLock is off")
}

// disableScreenLock 交互式关闭屏幕锁定（需要用户输入登录密码）
func disableScreenLock(cmd *cobra.Command) {
	if isScreenLockOff() {
		fmt.Fprintf(cmd.OutOrStdout(), "screen lock is already off\n")
		return
	}

	fmt.Fprintf(cmd.OutOrStdout(), "\nscreen lock is enabled — this will break AX automation when remote session disconnects\n")
	fmt.Fprintf(cmd.OutOrStdout(), "disabling screen lock requires your login password:\n")

	// sysadminctl -screenLock off -password - 会自己交互式提示输入密码
	sysadmin := exec.Command("sysadminctl", "-screenLock", "off", "-password", "-")
	sysadmin.Stdin = os.Stdin
	sysadmin.Stdout = os.Stdout
	sysadmin.Stderr = os.Stderr
	if err := sysadmin.Run(); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: failed to disable screen lock: %v\n", err)
		fmt.Fprintf(cmd.ErrOrStderr(), "  you can do it manually: sysadminctl -screenLock off -password -\n")
		return
	}

	// 验证
	if isScreenLockOff() {
		fmt.Fprintf(cmd.OutOrStdout(), "screen lock disabled\n")
	} else {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: screen lock may still be enabled, check System Settings > Lock Screen\n")
	}
}

var setupCaffeinateCmd = &cobra.Command{
	Use:   "setup-caffeinate",
	Short: "安装 caffeinate LaunchAgent + 关闭屏幕锁定，防止 headless Mac 降级",
	Long: `一键配置 headless Mac 防睡眠/防锁屏，避免 WindowServer 降级导致 AX UI 自动化失败。

执行内容：
  1. 安装 LaunchAgent，登录时自动启动 caffeinate -dimsu（等效于防止系统/显示器/磁盘睡眠）
  2. 关闭屏幕锁定（sysadminctl -screenLock off，需要输入登录密码）

使用 pplx remove-caffeinate 卸载（仅卸载 LaunchAgent，不恢复锁屏设置）。

plist 路径: ~/Library/LaunchAgents/com.pplx.caffeinate.plist`,
	RunE: func(cmd *cobra.Command, args []string) error {
		plistPath := caffeinatePlistPath()

		// ── Step 1: Install caffeinate LaunchAgent ──

		dir := filepath.Dir(plistPath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("create LaunchAgents dir: %w", err)
		}
		if err := os.WriteFile(plistPath, []byte(caffeinatePlist), 0644); err != nil {
			return fmt.Errorf("write plist: %w", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", plistPath)

		// 先尝试 bootout 旧的（忽略错误，可能不存在）
		_ = exec.Command("launchctl", "bootout", fmt.Sprintf("gui/%d/%s", os.Getuid(), caffeinateLabel)).Run()

		// bootstrap 加载新的
		if out, err := exec.Command("launchctl", "bootstrap", fmt.Sprintf("gui/%d", os.Getuid()), plistPath).CombinedOutput(); err != nil {
			return fmt.Errorf("launchctl bootstrap: %s (%w)", strings.TrimSpace(string(out)), err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "loaded %s\n", caffeinateLabel)

		// 验证 caffeinate 是否在跑
		if err := exec.Command("pgrep", "-x", "caffeinate").Run(); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: caffeinate process not detected after load\n")
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "caffeinate -dimsu is running\n")
		}

		// ── Step 2: Disable screen lock ──

		disableScreenLock(cmd)

		return nil
	},
}

var removeCaffeinateCmd = &cobra.Command{
	Use:   "remove-caffeinate",
	Short: "卸载 caffeinate LaunchAgent",
	Long: `停止并卸载由 pplx setup-caffeinate 安装的 LaunchAgent。
注意：不会恢复屏幕锁定设置。如需重新启用，请手动执行：
  sysadminctl -screenLock immediate -password -`,
	RunE: func(cmd *cobra.Command, args []string) error {
		plistPath := caffeinatePlistPath()

		// bootout 停止服务
		if out, err := exec.Command("launchctl", "bootout", fmt.Sprintf("gui/%d/%s", os.Getuid(), caffeinateLabel)).CombinedOutput(); err != nil {
			// 可能没加载过，继续删文件
			fmt.Fprintf(cmd.ErrOrStderr(), "launchctl bootout: %s\n", strings.TrimSpace(string(out)))
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "unloaded %s\n", caffeinateLabel)
		}

		// 删除 plist 文件
		if err := os.Remove(plistPath); err != nil {
			if os.IsNotExist(err) {
				fmt.Fprintf(cmd.OutOrStdout(), "plist not found (already removed)\n")
			} else {
				return fmt.Errorf("remove plist: %w", err)
			}
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "removed %s\n", plistPath)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "\nnote: screen lock was not re-enabled. To restore:\n")
		fmt.Fprintf(cmd.OutOrStdout(), "  sysadminctl -screenLock immediate -password -\n")
		return nil
	},
}
