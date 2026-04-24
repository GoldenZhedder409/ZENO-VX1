# ZENO-VX1

<p align="center">  
<img src="https://img.shields.io/badge/Version-Extreme_Edition-red?style=for-the-badge" alt="Version">  
<img src="https://img.shields.io/badge/Language-Go_1.22-00ADD8?style=for-the-badge" alt="Go">  
<img src="https://img.shields.io/badge/Purpose-Security_Research-green?style=for-the-badge" alt="Purpose">  
<img src="https://img.shields.io/badge/License-Educational-yellow?style=for-the-badge" alt="License">  
</p>

---

### 🔗 What is this?
🛡️ ZENO VX - GO EDITION is a high-performance, multi-vector network stress-testing framework written in Go (Golang). Leveraging Go's native concurrency (goroutines), this tool allows security researchers to evaluate network resilience against high-concurrency traffic patterns with an integrated adaptive rate control system.

---

### 🔗 Disclaimer ⚠️
> [!WARNING]
> DISCLAIMER: This tool is for Educational Purposes Only. Unauthorized use against third-party systems is illegal and may result in criminal charges. The author and distributor assume no liability for misuse of this software.
>
🚀 Key Features
Multi-Vector Attack Simulation: Supports TCP, UDP, HTTP, SYN flooding, and Slowloris.
Adaptive Rate Control: Dynamically adjusts packet delay based on success/fail rates to bypass basic filtering.
Advanced Proxy Rotator: Supports automatic proxy health checks and rotation from proxy.txt (HTTP/HTTPS/SOCKS5).
High Efficiency: Built with Golang goroutines for lightweight, high-speed execution compared to Python.
Real-time Analytics: Integrated monitor displaying PPS (Packets Per Second), active proxies, and adaptive delay metrics.

---

### 🔗 Attack method 💢

| Mode       | Description                              |
|------------|------------------------------------------|
| TCP Test   | Simulates multiple TCP connections       |
| UDP Test   | Sends randomized datagram traffic        |
| HTTP Test  | Generates repeated HTTP requests         |
| Conn Test  | Rapid open/close connection simulation   |
| Slow Test  | Simulates long-lived connections         |

---

### 🔗 Requirement 📋
Prerequisites
* Go (Golang) 1.18 or higher.
* proxy.txt (Optional: for proxy rotation mode).
* Root/Administrator privileges (for optimal socket performance on some OS).
* OS: Linux (Ubuntu/Debian recommended), Windows, or macOS.

---

### 🔗 How to run 🚀
* Copy "https://github.com/GoldenZhedder409/ZENO-VX1.git" at your Terminal
* Contents of proxy.txt
* Run with command "ZENO-VX1"
* Fill with your target

---

### Support us!! 💰
You can support this small project by donating to our **Monero** account
"83EzZCumdrRHcHF4pLd4uJ6hHpzwB81eXKfJktn6s4PQ8gZSqjtuZEsTSksDXdhn2jQp8pD2fiE1GTf5ysWBZWh867FCNBC"
