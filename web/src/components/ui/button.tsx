// Adapted from shadcn/ui (MIT): https://ui.shadcn.com/docs/components/button
import * as React from "react";
import { Slot } from "@radix-ui/react-slot";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "../../lib/utils";

const variants = cva("inline-flex items-center justify-center gap-2 whitespace-nowrap rounded-lg text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary disabled:pointer-events-none disabled:opacity-40", {
  variants: {
    variant: {
      default: "bg-primary text-slate-950 hover:bg-emerald-200",
      outline: "border border-border bg-transparent text-foreground hover:bg-slate-800",
      ghost: "text-muted hover:bg-slate-800 hover:text-foreground",
      destructive: "border border-red-400/30 bg-red-400/10 text-red-300 hover:bg-red-400/20",
    },
    size: { default: "h-10 px-4", sm: "h-8 px-3 text-xs", icon: "h-10 w-10" },
  },
  defaultVariants: { variant: "default", size: "default" },
});
export interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement>, VariantProps<typeof variants> { asChild?: boolean }
export const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(({ className, variant, size, asChild = false, ...props }, ref) => {
  const Comp = asChild ? Slot : "button";
  return <Comp className={cn(variants({ variant, size, className }))} ref={ref} {...props} />;
});
Button.displayName = "Button";
