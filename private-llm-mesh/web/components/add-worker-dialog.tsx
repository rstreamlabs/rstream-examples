"use client";

import { Plus } from "lucide-react";

import { CopyButton } from "@/components/copy-button";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";

const REPO = "https://github.com/rstreamlabs/rstream-examples.git";

export function AddWorkerDialog({
  projectEndpoint,
}: {
  projectEndpoint: string;
}) {
  const endpoint = projectEndpoint || "<project-endpoint>";
  const steps = [
    {
      label: "Authenticate and select the project",
      code: `rstream login\nrstream project use ${endpoint}`,
    },
    {
      label: "Build the worker (needs Go, CMake, a C/C++ compiler)",
      code: `git clone ${REPO}\ncd rstream-examples/private-llm-mesh/worker\nmake build`,
    },
    {
      label: "Serve a model to the pool",
      code: `./bin/worker --model qwen2.5:7b`,
    },
  ];
  return (
    <Dialog>
      <DialogTrigger
        render={
          <Button variant="outline" size="sm" className="gap-1.5">
            <Plus className="size-3.5" />
            Add worker
          </Button>
        }
      />
      <DialogContent className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Add a worker</DialogTitle>
          <DialogDescription>
            Run the worker on a machine with a GPU, or on CPU. It embeds
            llama.cpp and runs the model in-process, then joins the pool over
            rstream, with no public address and no port to open. The model
            downloads on first run. Install the rstream CLI from{" "}
            <a href="https://rstream.io" target="_blank" rel="noreferrer">
              rstream.io
            </a>
            .
          </DialogDescription>
        </DialogHeader>
        <ol className="flex flex-col gap-4">
          {steps.map((step, index) => (
            <li key={step.label} className="space-y-2">
              <p className="text-sm font-medium text-foreground">
                {index + 1}. {step.label}
              </p>
              <div className="relative">
                <pre className="max-w-full overflow-x-auto whitespace-pre-wrap break-all rounded-md border border-border bg-background p-3 pr-11 text-xs leading-6 text-foreground">
                  <code>{step.code}</code>
                </pre>
                <CopyButton
                  text={step.code}
                  className="absolute right-1.5 top-1.5"
                />
              </div>
            </li>
          ))}
        </ol>
      </DialogContent>
    </Dialog>
  );
}
