# Linux Knob Controller

[![Release](https://github.com/user/repo/actions/workflows/release.yml/badge.svg)](https://github.com/user/repo/releases)
[![AUR](https://img.shields.io/aur/version/volume-knob-control-bin)](https://aur.archlinux.org/packages/volume-knob-control-bin)

---

## 功能

- **独占控制**：接管旋钮事件，消除与 GNOME/KDE 音量弹窗的逻辑冲突。
- **静音逻辑**：
  - **单设备**：切换静音/取消静音。
  - **多设备**：按下旋钮即在不同物理输出（如耳机/扬声器）间循环切换，并**自动取消静音**。

默认排除 HDMI/DP 音频

## 安装

### 1. Arch Linux (AUR)
```bash
yay -S volume-knob-control-bin
```
### 2. Debian / Ubuntu
从 Releases 下载最新的 .deb 包：
```bash
sudo apt install ./volume-knob-control_amd64.deb
```
### 3. 二进制文件
```bash 
chmod +x ./volume-control
./volume-control
```
### `./volume-control -help` 查看参数`

## 使用时注意：需要将用户加入 input 用户组，`sudo usermod -aG input $USER`