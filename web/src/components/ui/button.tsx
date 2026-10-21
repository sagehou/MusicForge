// Adapted from shadcn/ui (MIT): https://ui.shadcn.com/docs/components/button
import * as React from "react";
import { Slot } from "@radix-ui/react-slot";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "../../lib/utils";

const variants = cva("ui-button inline-flex items-center justify-center gap-2 text-sm font-medium", {
  variants: {
    variant: {
      default: "button-primary",
      outline: "button-outline",
      ghost: "button-ghost",
      destructive: "button-destructive",
    },
    size: { default: "button-default", sm: "button-small", icon: "button-icon" },
  },
  defaultVariants: { variant: "default", size: "default" },
});
export interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement>, VariantProps<typeof variants> { asChild?: boolean }
export const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(({ className, variant, size, asChild = false, ...props }, ref) => {
  const Comp = asChild ? Slot : "button";
  return <Comp className={cn(variants({ variant, size, className }))} ref={ref} {...props} title={props.title ?? props["aria-label"]} />;
});
Button.displayName = "Button";
