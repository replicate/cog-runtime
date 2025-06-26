#!/usr/bin/env -S deno run --allow-read --allow-write --allow-net --allow-env

import { parse } from "https://deno.land/std@0.220.0/flags/mod.ts";
import { join } from "https://deno.land/std@0.220.0/path/mod.ts";
import { exists } from "https://deno.land/std@0.220.0/fs/mod.ts";

interface Config {
  module_name: string;
  predictor_name: string;
  max_concurrency: number;
}

interface PredictionRequest {
  id: string;
  input: Record<string, unknown>;
  created_at?: string;
  started_at?: string;
  webhook?: string;
  webhook_events_filter?: string[];
  output_file_prefix?: string;
}

interface PredictionResponse {
  id: string;
  input: Record<string, unknown>;
  output?: unknown;
  logs?: string;
  error?: string;
  status: "starting" | "processing" | "succeeded" | "failed" | "canceled";
  created_at?: string;
  started_at?: string;
  completed_at?: string;
  metrics?: Record<string, unknown>;
}

class Logger {
  private pid?: string;

  setPid(pid: string | undefined) {
    this.pid = pid;
  }

  log(message: string) {
    if (this.pid) {
      console.log(`[pid=${this.pid}] ${message}`);
    } else {
      console.log(message);
    }
  }

  error(message: string) {
    if (this.pid) {
      console.error(`[pid=${this.pid}] ${message}`);
    } else {
      console.error(message);
    }
  }
}

const logger = new Logger();

interface ClassPredictor {
  (): void;
  predict(input: unknown): unknown;
}

type PredictFn = (input: unknown) => unknown;

class FileRunner {
  private workingDir: string;
  private ipcUrl: string;
  private config?: Config;
  private predictfn: PredictFn = () => { throw new Error("Not implemented") };
  private running = new Map<string, boolean>();
  private stopped = false;

  constructor(workingDir: string, ipcUrl: string) {
    this.workingDir = workingDir;
    this.ipcUrl = ipcUrl;
  }

  async start() {
    logger.log("[coglet] Starting file runner");

    // Wait for config
    await this.waitForConfig();

    // Load predictor
    await this.loadPredictor();

    // Run setup if available
    await this.runSetup();

    // Send ready status
    await this.sendStatus("READY");

    // Start monitoring loop
    await this.monitorLoop();
  }

  private async waitForConfig() {
    const configPath = join(this.workingDir, "config.json");
    logger.log(`[coglet] Waiting for config at ${configPath}`);

    while (!await exists(configPath)) {
      await new Promise(resolve => setTimeout(resolve, 100));
    }

    const configData = await Deno.readTextFile(configPath);
    this.config = JSON.parse(configData);
    logger.log(`[coglet] Loaded config: ${JSON.stringify(this.config)}`);
  }

  private async loadPredictor() {
    if (!this.config) throw new Error("Config not loaded");

    try {
      // Import the module dynamically
      const modulePath = new URL(this.config.module_name, `file://${Deno.cwd()}/`).href;
      const module = await import(modulePath);

      // Get the predictor (class or function)
      try {
        const instance = (new module[this.config.predictor_name]);
        this.predictfn = instance.predict.bind(instance)
      } catch (err) {
        this.predictfn = module[this.config.predictor_name];
      }

      if (!this.predictfn) {
        throw new Error(`Predictor ${this.config.predictor_name} not found in module`);
      }

      logger.log(`[coglet] Loaded predictor: ${this.config.predictor_name}`);
    } catch (error) {
      logger.error(`[coglet] Failed to load predictor: ${error}`);
      throw error;
    }
  }

  private async runSetup() {
    const setupResult: { status: string, started_at: string, completed_at?: string, logs: string } = {
      status: "started",
      started_at: new Date().toISOString().replace("Z", "+00:00"),
      logs: "",
    };

    try {
      setupResult.status = "succeeded";
      setupResult.completed_at = new Date().toISOString().replace("Z", "+00:00");
    } catch (error) {
      logger.error(`[coglet] Setup failed: ${error}`);
      setupResult.status = "failed";
      setupResult.completed_at = new Date().toISOString().replace("Z", "+00:00");
      setupResult.logs += `Setup error: ${error}\n`;
    }

    // Write setup result
    await Deno.writeTextFile(
      join(this.workingDir, "setup_result.json"),
      JSON.stringify(setupResult)
    );

    // Write OpenAPI schema (simplified version)
    const schema = {
      openapi: "3.0.0",
      info: { title: "Prediction API", version: "1.0.0" },
      paths: {},
      components: {
        schemas: {
          Input: {
            type: "object",
            additionalProperties: true,
          }
        }
      }
    };
    await Deno.writeTextFile(
      join(this.workingDir, "openapi.json"),
      JSON.stringify(schema)
    );
  }

  private async sendStatus(status: string) {
    try {
      await fetch(this.ipcUrl, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ status }),
      });
    } catch (error) {
      logger.error(`[coglet] Failed to send status ${status}: ${error}`);
    }
  }

  private async monitorLoop() {
    logger.log("[coglet] Starting monitor loop");

    while (!this.stopped) {
      try {
        // Check for stop file
        if (await exists(join(this.workingDir, "stop"))) {
          logger.log("[coglet] Stop file detected, shutting down");
          this.stopped = true;
          break;
        }

        // Read directory entries
        const entries = [];
        for await (const entry of Deno.readDir(this.workingDir)) {
          entries.push(entry);
        }

        // Process request files
        for (const entry of entries) {
          if (entry.isFile && entry.name.startsWith("request-") && entry.name.endsWith(".json")) {
            const id = entry.name.replace("request-", "").replace(".json", "");

            if (!this.running.has(id) && this.running.size < this.config!.max_concurrency) {
              this.processRequest(id);
            }
          }
        }

        // Update status based on capacity
        const isBusy = this.running.size >= this.config!.max_concurrency;
        await this.sendStatus(isBusy ? "BUSY" : "READY");

      } catch (error) {
        logger.error(`[coglet] Monitor loop error: ${error}`);
      }

      await new Promise(resolve => setTimeout(resolve, 100));
    }
  }

  private async processRequest(id: string) {
    this.running.set(id, true);
    logger.setPid(id);

    const requestPath = join(this.workingDir, `request-${id}.json`);
    const cancelPath = join(this.workingDir, `cancel-${id}`);

    try {
      // Read request
      const requestData = await Deno.readTextFile(requestPath);
      const request: PredictionRequest = JSON.parse(requestData);

      // Delete request file
      await Deno.remove(requestPath);

      logger.log(`Processing prediction`);

      // Initialize response
      let response: PredictionResponse = {
        id: request.id,
        input: request.input,
        status: "starting",
        created_at: request.created_at,
        started_at: request.started_at || new Date().toISOString().replace("Z", "+00:00"),
        logs: "",
      };

      // Write initial response
      await this.writeResponse(id, response, 0);

      // Check for cancellation
      if (await exists(cancelPath)) {
        response.status = "canceled";
        response.completed_at = new Date().toISOString().replace("Z", "+00:00");
        await this.writeResponse(id, response, 1);
        return;
      }

      // Update status to processing
      response.status = "processing";
      await this.writeResponse(id, response, 1);

      // Run prediction
      try {
        const output = await this.predictfn(request.input);
        response.output = output;
        response.status = "succeeded";
      } catch (error) {
        logger.error(`Prediction error: ${error}`);
        response.error = String(error);
        response.status = "failed";
      }

      // Final response
      response.completed_at = new Date().toISOString().replace("Z", "+00:00");
      await this.writeResponse(id, response, 2);

    } catch (error) {
      logger.error(`Failed to process request: ${error}`);
    } finally {
      this.running.delete(id);
      logger.setPid(undefined);

      // Clean up cancel file if exists
      try {
        if (await exists(cancelPath)) {
          await Deno.remove(cancelPath);
        }
      } catch { }
    }
  }

  private async writeResponse(id: string, response: PredictionResponse, epoch: number) {
    const filename = `response-${id}-${epoch}.json`;
    await Deno.writeTextFile(
      join(this.workingDir, filename),
      JSON.stringify(response)
    );
    await this.sendStatus("OUTPUT");
  }
}

// Main entry point
async function main() {
  const args = parse(Deno.args, {
    string: ["ipc-url", "working-dir"],
  });

  if (!args["ipc-url"] || !args["working-dir"]) {
    console.error("Usage: coglet.ts --ipc-url <url> --working-dir <dir>");
    Deno.exit(1);
  }

  const runner = new FileRunner(args["working-dir"], args["ipc-url"]);
  await runner.start();
}

if (import.meta.main) {
  main();
}
