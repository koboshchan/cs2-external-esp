/*
    This project is licensed under CC BY-NC 4.0
    https://creativecommons.org/licenses/by-nc/4.0*

    You are free to:
    • Share: Copy and redistribute the material in any medium or format.
    • Adapt: Remix, transform, and build upon the material*

    Under the following terms:
    • Attribution: You must give appropriate credit, provide a link to the original source repository, and indicate if changes were made.
    • Non-Commercial: You may not use the material for commercial purposes*

    You are not allowed to:
    • Sell: This license forbids selling original or modified material for commercial purposes.
    • Sublicense: This license forbids sublicensing original or modified material.

    © Copyright by IMXNOOBX (https://github.com/IMXNOOBX) and contributors.
    See https://github.com/IMXNOOBX/cs2-external-esp/-/blob/main/LICENSE for full details.
*/

#include <algorithm>
#include <cstdlib>
#include <iostream>
#include <string>

#include "updater/Updater.hpp"
#include "core/engine/Engine.hpp"
#include "gui/renderer/Renderer.hpp"
#include "net/publisher.hpp"

#include <external/exception.hpp>

namespace
{
	struct Options
	{
		std::string mode = "local"; // local | host | viewer
		bool self_test = false;
		int self_test_seconds = 10;

		std::string url;
		std::string room;
		std::string token;

		bool help = false;
	};

	bool StartsWith(const std::string& value, const char* prefix)
	{
		return value.rfind(prefix, 0) == 0;
	}

	std::string ValueAfter(const std::string& arg, const char* prefix)
	{
		return arg.substr(std::string(prefix).size());
	}

	Options ParseArgs(int argc, char** argv)
	{
		Options o;

		for (int i = 1; i < argc; i++)
		{
			const std::string arg = argv[i];

			if (arg == "--help" || arg == "-h")
				o.help = true;
			else if (StartsWith(arg, "--mode="))
				o.mode = ValueAfter(arg, "--mode=");
			else if (arg == "--net-selftest")
				o.self_test = true;
			else if (StartsWith(arg, "--net-selftest="))
			{
				o.self_test = true;
				o.self_test_seconds = std::max(1, std::atoi(ValueAfter(arg, "--net-selftest=").c_str()));
			}
			else if (StartsWith(arg, "--url="))
				o.url = ValueAfter(arg, "--url=");
			else if (StartsWith(arg, "--room="))
				o.room = ValueAfter(arg, "--room=");
			else if (StartsWith(arg, "--token="))
				o.token = ValueAfter(arg, "--token=");
			else
				LOGF(WARNING, "Ignoring unknown argument: {}", arg);
		}

		return o;
	}

	void PrintHelp()
	{
		std::cout
			<< "cs2-external-esp\n\n"
			<< "  --mode=local           read memory and render locally (default)\n"
			<< "  --mode=host            read memory, render locally and publish to a relay\n"
			<< "  --mode=viewer          not part of this binary (use the Go viewer)\n\n"
			<< "  --url=ws://host:8080/ws   relay endpoint\n"
			<< "  --room=ABC123             relay room\n"
			<< "  --token=...               host token from POST /rooms\n\n"
			<< "  --net-selftest[=SECONDS]  publish synthetic frames, no game required\n"
			<< std::endl;
	}

	void ApplyOverrides(const Options& o)
	{
		if (!o.url.empty()) cfg::remote::url = o.url;
		if (!o.room.empty()) cfg::remote::room = o.room;
		if (!o.token.empty()) cfg::remote::token = o.token;
	}
}

int main(int argc, char** argv)
{
	const Options options = ParseArgs(argc, argv);

	if (options.help)
	{
		PrintHelp();
		return 0;
	}

	c_exception_handler::setup();

	LogHelper::Init();

	LOGF(INFO, "Compiled {}, Welcome to cs2-external-esp-recode!", __TIMESTAMP__);

	// Transport self-test: no game, no overlay, just frames on the wire.
	if (options.self_test)
	{
		if (options.url.empty() || options.room.empty() || options.token.empty())
		{
			LOGF(FATAL, "--net-selftest needs --url=, --room= and --token=");
			LogHelper::Destroy();
			return 1;
		}

		const int code = net::Publisher::RunSelfTest(
			options.url,
			options.room,
			options.token,
			options.self_test_seconds
		);

		LogHelper::Destroy();
		return code;
	}

	if (options.mode == "viewer")
	{
		LOGF(FATAL, "--mode=viewer is not part of this binary; run the Go viewer instead");
		LogHelper::Destroy();
		return 1;
	}

	if (options.mode != "local" && options.mode != "host")
	{
		LOGF(FATAL, "Unknown --mode={} (expected local, host or viewer)", options.mode);
		LogHelper::Destroy();
		return 1;
	}

	// Needs to be ran as ADMINISTRATOR
	if (!SetPriorityClass(GetCurrentProcess(), HIGH_PRIORITY_CLASS))
		LOGF(WARNING, "Could not set application process priority to HIGH");

	if (!Updater::Init() || !Updater::Process()) {
		LOGF(FATAL, "Updater failed to run, the application has not verified its status, execution its not recommended");
		LOGF(INFO, "Press any key to ignore and continue execution...");
		std::cin.get();
	}

	if (!Engine::Init()) {
		LOGF(FATAL, "Engine failed to initialize, cannot continue execution");
		goto exit;
	}

	// Config is loaded inside Engine::Init, so command line values win here.
	ApplyOverrides(options);

	if (options.mode == "host")
	{
		if (!net::Publisher::Start())
			LOGF(WARNING, "Remote publishing is not active");
	}

	if (!Renderer::Init()) {
		LOGF(FATAL, "Renderer failed to initialize, cannot continue execution");
		goto exit;
	}

	LOGF(INFO, "Everything setup and ready, just... make sure you are not in \"Full Screen\"!");

	// Locking
	Renderer::Thread();

exit:
	net::Publisher::Stop();
	LOGF(INFO, "Thats it, im done, hope you had a great time!");
	LogHelper::Destroy();
	std::cin.get();
}
