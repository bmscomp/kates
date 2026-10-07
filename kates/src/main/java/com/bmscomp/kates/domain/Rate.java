package com.bmscomp.kates.domain;

import java.lang.annotation.Documented;
import java.lang.annotation.ElementType;
import java.lang.annotation.Retention;
import java.lang.annotation.RetentionPolicy;
import java.lang.annotation.Target;
import jakarta.validation.Constraint;
import jakarta.validation.Payload;

/**
 * A rate in records a second: -1, which is unlimited, or 1 or more. Null, a
 * rate left unset, passes.
 *
 * <p>Both benchmark backends run a rate below 1 unthrottled, so a 0 ran flat
 * out. {@code @Min(-1)}, which held a spec's rates before, let it through,
 * although its message said only -1 or a positive rate would do.
 */
@Documented
@Constraint(validatedBy = RateValidator.class)
@Target(ElementType.FIELD)
@Retention(RetentionPolicy.RUNTIME)
public @interface Rate {

    String message() default "must be -1 (unlimited) or positive";

    Class<?>[] groups() default {};

    Class<? extends Payload>[] payload() default {};
}
