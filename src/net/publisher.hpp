#pragma once

// Host side of the remote ESP: serializes the cache into protocol frames and
// publishes them to the relay. Runs on its own thread so a slow or dead
// connection can never stall the engine.

#include <atomic>
#include <cstdint>
#include <mutex>
#include <string>
#include <thread>

namespace net
{
	struct PublisherStats
	{
		bool running = false;
		bool connected = false;
		uint64_t frames_sent = 0;
		uint64_t bytes_sent = 0;
		uint64_t dropped = 0;
		int viewers = -1; // -1 = unknown (older relay)
		int last_error = 0;
		std::string last_message;
	};

	class Publisher
	{
	public:
		static bool Start();
		static void Stop();
		static bool IsRunning();
		static PublisherStats GetStats();

		// Publishes synthetic frames without touching the game. Used to verify
		// the transport end to end on a machine that is not running CS2.
		static int RunSelfTest(const std::string& url, const std::string& room, const std::string& token, int seconds);

	private:
		static Publisher& Get();

		void Thread();
		bool ConnectOnce();
		bool PublishFrame();
		void SleepUntilNextTick();

		Publisher() = default;
		~Publisher();

		std::thread thread_;
		std::atomic<bool> running_{ false };

		std::string url_;
		std::string room_;
		std::string token_;
		int rate_hz_ = 30;

		uint32_t seq_ = 0;
		std::atomic<bool> connected_{ false };
		std::atomic<bool> authenticated_{ false };
		std::atomic<bool> rejected_{ false };
		std::atomic<uint64_t> frames_sent_{ 0 };
		std::atomic<uint64_t> bytes_sent_{ 0 };
		std::atomic<uint64_t> dropped_{ 0 };
		std::atomic<int> last_error_{ 0 };
		mutable std::mutex message_mutex_;
		std::string last_message_;
	};
}
